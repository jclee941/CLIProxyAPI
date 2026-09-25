package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const webUploadOrigin = "https://push.clients6.google.com"

// webAttachment is one uploaded file as the generation request carries it.
type webMedia struct {
	MIMEType string
	Data     string
	// Reference names a file whose bytes are fetched before upload instead of
	// arriving inline. It is resolved away by resolveMedia.
	Reference string
}

// webInlinePart is the typed form of the media a request part carries. The media
// type travels under either spelling depending on which bridge built the
// request, and both are valid Gemini REST.
type webInlinePart struct {
	MIMEType  string `json:"mimeType"`
	MIMESnake string `json:"mime_type"`
	Data      string `json:"data"`
}

// webFilePart is the typed form of a part that names a file instead of carrying
// it, under either spelling of the uri.
type webFilePart struct {
	FileURI   string `json:"fileUri"`
	FileSnake string `json:"file_uri"`
	MIMEType  string `json:"mimeType"`
	MIMESnake string `json:"mime_type"`
}

func (part webFilePart) uri() string {
	if part.FileURI != "" {
		return part.FileURI
	}
	return part.FileSnake
}

func (part webInlinePart) mimeType() string {
	if part.MIMEType != "" {
		return part.MIMEType
	}
	return part.MIMESnake
}

type webAttachment struct {
	Path     string
	Name     string
	MIMEType string
	ClientID string
}

// webUploadLimit bounds a single attachment. The bytes stream from their source
// into the upload rather than being held, so this bounds one transfer rather
// than the process, and an inline attachment is bounded long before it by the
// request size the caller is allowed to send at all.
const webUploadLimit = 100 * 1024 * 1024

// webSource is one attachment as the upload path consumes it: the media type and
// the length the resumable protocol has to declare before the bytes move, and
// the bytes themselves, opened only once the upload is ready to take them.
type webSource struct {
	MIMEType string
	Size     int64
	Open     func(context.Context) (io.ReadCloser, error)
}

// upload stores one file with Google's resumable protocol: the first call
// declares the size and answers with a per-upload URL, the second sends the
// bytes and finalises. The reply body is the contrib_service path the
// generation request references.
func (session *webSession) upload(ctx context.Context, name, mimeType string, size int64, content io.Reader) (webAttachment, error) {
	if size <= 0 || size > webUploadLimit {
		return webAttachment{}, failure(400, "attachment_size_rejected")
	}
	start, err := http.NewRequestWithContext(ctx, http.MethodPost, session.uploadOrigin+"/upload/", strings.NewReader("File name: "+name))
	if err != nil {
		return webAttachment{}, failure(400, "web_request_invalid")
	}
	start.Header = session.uploadHeaders()
	start.Header.Set("X-Goog-Upload-Command", "start")
	start.Header.Set("X-Goog-Upload-Protocol", "resumable")
	start.Header.Set("X-Goog-Upload-Header-Content-Length", strconv.FormatInt(size, 10))
	start.Header.Set("X-Tenant-Id", "bard-storage")
	start.Header.Set("Push-ID", webUploadFeed)
	start.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	started, err := session.client.Do(start)
	if err != nil {
		return webAttachment{}, failure(502, "attachment_upload_transport_failed")
	}
	location := started.Header.Get("X-Goog-Upload-Url")
	if drainErr := drainResponse(started); drainErr != nil {
		return webAttachment{}, drainErr
	}
	if started.StatusCode != http.StatusOK || location == "" {
		return webAttachment{}, failure(502, "attachment_upload_rejected")
	}
	// The source is trusted for the length it declared, never for the length it
	// delivers: the reader is cut at the declared size so a source that keeps
	// talking cannot stretch the transfer past what upstream was told to expect.
	finalise, err := http.NewRequestWithContext(ctx, http.MethodPost, location, io.LimitReader(content, size))
	if err != nil {
		return webAttachment{}, failure(400, "web_request_invalid")
	}
	finalise.ContentLength = size
	finalise.Header = session.uploadHeaders()
	finalise.Header.Set("X-Tenant-Id", "bard-storage")
	finalise.Header.Set("Push-ID", webUploadFeed)
	finalise.Header.Set("X-Goog-Upload-Command", "upload, finalize")
	finalise.Header.Set("X-Goog-Upload-Offset", "0")
	response, err := session.client.Do(finalise)
	if err != nil {
		return webAttachment{}, failure(502, "attachment_upload_transport_failed")
	}
	session.absorb(response)
	body, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if closeErr := response.Body.Close(); closeErr != nil {
		_ = closeErr
	}
	if err != nil || response.StatusCode != http.StatusOK {
		return webAttachment{}, failure(502, "attachment_upload_rejected")
	}
	path := strings.TrimSpace(string(body))
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, " \t\r\n") {
		return webAttachment{}, failure(502, "attachment_reference_invalid")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return webAttachment{}, failure(500, "attachment_id_failed")
	}
	return webAttachment{Path: path, Name: name, MIMEType: mimeType, ClientID: hex.EncodeToString(random[:])}, nil
}

const webUploadFeed = "feeds/mcudyrk2a4khkz"

func (session *webSession) uploadHeaders() http.Header {
	headers := http.Header{
		"Cookie":     {session.cookie},
		"Origin":     {session.origin},
		"User-Agent": {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"},
	}
	if session.prefix != "" {
		headers.Set("X-Goog-AuthUser", strings.TrimPrefix(session.prefix, "/u/"))
	}
	return headers
}

func drainResponse(response *http.Response) error {
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20)); err != nil {
		if closeErr := response.Body.Close(); closeErr != nil {
			_ = closeErr
		}
		return failure(502, "attachment_upload_rejected")
	}
	if err := response.Body.Close(); err != nil {
		return failure(502, "attachment_upload_rejected")
	}
	return nil
}

// webAttachmentSlot renders the uploaded files into the positional entry the
// prompt slot carries at index 3.
func webAttachmentSlot(attachments []webAttachment) []any {
	if len(attachments) == 0 {
		return nil
	}
	entries := make([]any, 0, len(attachments))
	for _, attachment := range attachments {
		entries = append(entries, []any{
			[]any{attachment.Path, 1, nil, attachment.MIMEType, attachment.ClientID},
			attachment.Name, nil, nil, nil, nil, nil, nil, []any{0},
		})
	}
	return entries
}

// uploadMedia stores each inline part the caller sent so the generation request
// can reference them. A base64 part is decoded once here rather than carried
// through the prompt path.
func (session *webSession) uploadSources(ctx context.Context, sources []webSource) ([]webAttachment, error) {
	if len(sources) > webUploadCount {
		return nil, failure(400, "attachment_count_rejected")
	}
	attachments := make([]webAttachment, 0, len(sources))
	for index, source := range sources {
		mimeType := webBaseMIME(source.MIMEType)
		kind, supported := webUploadKinds[mimeType]
		if !supported {
			return nil, failure(400, "attachment_type_unsupported")
		}
		if source.Size <= 0 || source.Size > webUploadLimit {
			return nil, failure(400, "attachment_size_rejected")
		}
		attachment, err := session.uploadSource(ctx, "attachment-"+strconv.Itoa(index+1)+kind.extension, mimeType, source)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, attachment)
	}
	return attachments, nil
}

func (session *webSession) uploadSource(ctx context.Context, name, mimeType string, source webSource) (webAttachment, error) {
	content, err := source.Open(ctx)
	if err != nil {
		return webAttachment{}, err
	}
	defer func() {
		if closeErr := content.Close(); closeErr != nil {
			_ = closeErr
		}
	}()
	return session.upload(ctx, name, mimeType, source.Size, content)
}

// inlineSource carries an attachment the caller wrote into the request. Those
// bytes arrived in memory with the request already, so they are decoded here,
// where a malformed encoding is still the caller's error rather than a transfer
// that dies halfway through with nothing useful to say.
func inlineSource(item webMedia) (webSource, error) {
	content, err := base64.StdEncoding.DecodeString(item.Data)
	if err != nil {
		return webSource{}, failure(400, "attachment_encoding_invalid")
	}
	return webSource{MIMEType: item.MIMEType, Size: int64(len(content)), Open: func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(content)), nil
	}}, nil
}

// webUploadCount bounds the uploads one request can start. Each attachment costs
// two round trips, and the documented reference-image examples top out at six.
const webUploadCount = 16

// webUploadKinds is what the web product accepts as an attachment, with the name
// suffix it is stored under and the line the prompt announces it by. A type
// outside this set is refused before the upload, because upstream answers an
// unsupported file with a reference error that names nothing.
var webUploadKinds = map[string]struct {
	extension string
	notice    string
}{
	"image/png":       {".png", "[Image attached]"},
	"image/jpeg":      {".jpg", "[Image attached]"},
	"image/webp":      {".webp", "[Image attached]"},
	"application/pdf": {".pdf", "[Document attached]"},
	"text/plain":      {".txt", "[Document attached]"},
	"text/csv":        {".csv", "[Document attached]"},
	"text/markdown":   {".md", "[Document attached]"},
	"video/mp4":       {".mp4", "[Video attached]"},
	"video/webm":      {".webm", "[Video attached]"},
}

// webBaseMIME drops the parameters a caller may hang off a media type, so that
// "text/plain; charset=utf-8" is recognised as text/plain.
func webBaseMIME(mimeType string) string {
	base, _, _ := strings.Cut(mimeType, ";")
	return strings.ToLower(strings.TrimSpace(base))
}

// webAttachmentNotice is what the prompt carries in place of the file, so the
// model is told what was attached even though the bytes travel beside the text.
func webAttachmentNotice(mimeType string) string {
	if kind, supported := webUploadKinds[webBaseMIME(mimeType)]; supported {
		return kind.notice
	}
	return "[Attachment]"
}
