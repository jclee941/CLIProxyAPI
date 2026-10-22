package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// The web app attaches a file to a turn in three steps: it asks the file service
// for an entry, puts the bytes on the blob store, and has the service process the
// file; only then does the message reference it. Audio and video take the same
// route as any other multimodal upload, so the bytes the caller sent are the bytes
// the product holds.

const (
	webAttachmentMaxFiles = 10
	webUploadUseCase      = "multimodal"
	webUploadSource       = "composer"
	webBlobVersion        = "2020-04-08"
)

// webAttachmentMaxBytes bounds one decoded attachment; the product's own ceiling
// is 512 MiB, but a request body this plugin has to hold in memory is smaller.
var webAttachmentMaxBytes = 100 << 20

type webAttachment struct {
	Name string
	MIME string
	Data []byte
}

type webUploadedFile struct {
	ID   string
	Name string
	MIME string
	Size int
}

type webMediaType struct {
	mime       string
	extensions []string
	aliases    []string
	matches    func([]byte) bool
}

// webMediaTypes lists the formats whose upload and use were verified against the
// live product; every other type is refused instead of being sent on faith.
var webMediaTypes = []webMediaType{
	{mime: "audio/wav", extensions: []string{"wav"}, aliases: []string{"audio/x-wav", "audio/wave", "audio/vnd.wave"}, matches: isWaveData},
	{mime: "audio/mpeg", extensions: []string{"mp3"}, aliases: []string{"audio/mp3"}, matches: isMP3Data},
	{mime: "video/mp4", extensions: []string{"mp4"}, matches: isMP4Data},
}

func isWaveData(data []byte) bool {
	return len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WAVE"
}

func isMP3Data(data []byte) bool {
	if bytes.HasPrefix(data, []byte("ID3")) {
		return true
	}
	return len(data) >= 2 && data[0] == 0xff && data[1]&0xe0 == 0xe0
}

func isMP4Data(data []byte) bool {
	return len(data) >= 12 && string(data[4:8]) == "ftyp"
}

func webMediaByMIME(mime string) (webMediaType, bool) {
	mime = strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]))
	for _, media := range webMediaTypes {
		if media.mime == mime {
			return media, true
		}
		for _, alias := range media.aliases {
			if alias == mime {
				return media, true
			}
		}
	}
	return webMediaType{}, false
}

func webMediaByExtension(extension string) (webMediaType, bool) {
	extension = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(extension), "."))
	for _, media := range webMediaTypes {
		for _, known := range media.extensions {
			if known == extension {
				return media, true
			}
		}
	}
	return webMediaType{}, false
}

func unsupportedAttachment() error { return failure(400, "unsupported_chat_attachment") }

func invalidAttachment() error { return failure(400, "invalid_chat_attachment") }

// webDecodeAttachment turns a base64 payload into a checked attachment. The bytes
// must look like the type they are declared as, so a mislabelled file is refused
// here and never reaches the product.
func webDecodeAttachment(name string, media webMediaType, encoded string, position int, fallbackStem string) (webAttachment, error) {
	if len(encoded) > base64.StdEncoding.EncodedLen(webAttachmentMaxBytes)+len(encoded)/76+4 {
		return webAttachment{}, failure(http.StatusRequestEntityTooLarge, "chat_attachment_too_large")
	}
	data, err := base64.StdEncoding.DecodeString(strings.NewReplacer("\r", "", "\n", "").Replace(encoded))
	if err != nil || len(data) == 0 {
		return webAttachment{}, invalidAttachment()
	}
	if len(data) > webAttachmentMaxBytes {
		return webAttachment{}, failure(http.StatusRequestEntityTooLarge, "chat_attachment_too_large")
	}
	if !media.matches(data) {
		return webAttachment{}, invalidAttachment()
	}
	return webAttachment{Name: webAttachmentName(name, fallbackStem, position, media), MIME: media.mime, Data: data}, nil
}

func webAttachmentName(name, stem string, position int, media webMediaType) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, path.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")))
	if name == "" || name == "." || name == "/" {
		return fmt.Sprintf("%s-%d.%s", stem, position, media.extensions[0])
	}
	if len(name) > 255 {
		name = name[len(name)-255:]
	}
	return name
}

// webSplitDataURL separates a base64 data URL into its media type and payload.
func webSplitDataURL(value string) (string, string, bool) {
	header, payload, found := strings.Cut(strings.TrimSpace(value), ",")
	if !found || !strings.HasPrefix(header, "data:") || !strings.HasSuffix(strings.ToLower(header), ";base64") {
		return "", "", false
	}
	return header[len("data:") : len(header)-len(";base64")], payload, true
}

type webChatPart struct {
	Type       string `json:"type"`
	Text       string `json:"text"`
	InputAudio *struct {
		Data   string `json:"data"`
		Format string `json:"format"`
	} `json:"input_audio"`
	File *struct {
		Filename string `json:"filename"`
		FileData string `json:"file_data"`
	} `json:"file"`
	VideoURL *struct {
		URL string `json:"url"`
	} `json:"video_url"`
}

// attachment decodes one media part. position is the 1-based place the clip takes
// among the turn's attachments, which names a clip that arrives without a name.
func (part webChatPart) attachment(position int) (webAttachment, error) {
	switch part.Type {
	case "input_audio":
		if part.InputAudio == nil || part.InputAudio.Data == "" {
			return webAttachment{}, unsupportedAttachment()
		}
		media, ok := webMediaByExtension(part.InputAudio.Format)
		if !ok || !strings.HasPrefix(media.mime, "audio/") {
			return webAttachment{}, unsupportedAttachment()
		}
		return webDecodeAttachment("", media, part.InputAudio.Data, position, "audio")
	case "file":
		if part.File == nil || part.File.FileData == "" {
			return webAttachment{}, unsupportedAttachment()
		}
		declared, payload, isDataURL := webSplitDataURL(part.File.FileData)
		media, ok := webMediaByMIME(declared)
		if !isDataURL {
			payload = part.File.FileData
			media, ok = webMediaByExtension(path.Ext(part.File.Filename))
		}
		if !ok {
			return webAttachment{}, unsupportedAttachment()
		}
		return webDecodeAttachment(part.File.Filename, media, payload, position, "attachment")
	case "video_url":
		if part.VideoURL == nil {
			return webAttachment{}, unsupportedAttachment()
		}
		declared, payload, isDataURL := webSplitDataURL(part.VideoURL.URL)
		media, ok := webMediaByMIME(declared)
		if !isDataURL || !ok || !strings.HasPrefix(media.mime, "video/") {
			return webAttachment{}, unsupportedAttachment()
		}
		return webDecodeAttachment("", media, payload, position, "video")
	default:
		return webAttachment{}, unsupportedAttachment()
	}
}

// uploadAttachments sends the files one at a time so the ids come back in the
// order the caller gave the files.
func (client *webClient) uploadAttachments(ctx context.Context, attachments []webAttachment) ([]webUploadedFile, error) {
	files := make([]webUploadedFile, 0, len(attachments))
	for _, attachment := range attachments {
		file, err := client.uploadAttachment(ctx, attachment)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func (client *webClient) uploadAttachment(ctx context.Context, attachment webAttachment) (webUploadedFile, error) {
	created, err := client.createUpload(ctx, attachment)
	if err != nil {
		return webUploadedFile{}, err
	}
	if err := client.putBlob(ctx, created.UploadURL, created.UploadHeaders, attachment); err != nil {
		return webUploadedFile{}, err
	}
	if err := client.processUpload(ctx, created.FileID, attachment); err != nil {
		return webUploadedFile{}, err
	}
	return webUploadedFile{ID: created.FileID, Name: attachment.Name, MIME: attachment.MIME, Size: len(attachment.Data)}, nil
}

type webCreatedUpload struct {
	Status        string            `json:"status"`
	FileID        string            `json:"file_id"`
	UploadURL     string            `json:"upload_url"`
	UploadHeaders map[string]string `json:"upload_headers"`
}

func (client *webClient) createUpload(ctx context.Context, attachment webAttachment) (webCreatedUpload, error) {
	path := "/backend-api/files"
	request, err := json.Marshal(map[string]any{
		"file_name":           attachment.Name,
		"file_size":           len(attachment.Data),
		"use_case":            webUploadUseCase,
		"mime_type":           attachment.MIME,
		"timezone_offset_min": -480,
		"reset_rate_limits":   false,
		"upload_source":       webUploadSource,
		"store_in_library":    false,
	})
	if err != nil {
		return webCreatedUpload{}, failure(500, "web_request_encoding_failed")
	}
	raw, err := client.call(ctx, http.MethodPost, path,
		client.header(path, map[string]string{"Content-Type": "application/json", "Accept": "*/*"}), request, "web_upload_create_failed")
	if err != nil {
		return webCreatedUpload{}, err
	}
	var created webCreatedUpload
	if json.Unmarshal(raw, &created) != nil || created.FileID == "" || created.UploadURL == "" {
		if created.Status == "error" {
			return webCreatedUpload{}, failure(422, "web_upload_rejected")
		}
		return webCreatedUpload{}, failure(502, "web_upload_create_invalid")
	}
	return created, nil
}

// putBlob writes the bytes to the pre-signed blob URL. The request may have been
// sent once the body is on the wire, so a transport failure is not retried. The
// blob store is not the ChatGPT backend: the request goes out on the same
// transport but without the cookie jar, and a grant never gets to set credential
// headers, so neither the bearer token nor a session cookie can reach it.
func (client *webClient) putBlob(ctx context.Context, uploadURL string, extra map[string]string, attachment webAttachment) error {
	target, err := url.Parse(uploadURL)
	if err != nil || target.Scheme != "https" || target.Host == "" {
		return failure(502, "web_upload_url_invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, target.String(), bytes.NewReader(attachment.Data))
	if err != nil {
		return failure(500, "web_request_invalid")
	}
	request.Header.Set("Content-Type", attachment.MIME)
	request.Header.Set("x-ms-blob-type", "BlockBlob")
	request.Header.Set("x-ms-version", webBlobVersion)
	for key, value := range extra {
		switch strings.ToLower(key) {
		case "content-length", "host", "authorization", "proxy-authorization", "cookie":
		default:
			request.Header.Set(key, value)
		}
	}
	blobHTTP := &http.Client{Transport: client.http.Transport, CheckRedirect: client.http.CheckRedirect}
	response, err := blobHTTP.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return failure(499, "web_client_disconnected")
		}
		return failure(502, "web_upload_transport_failed")
	}
	defer closeBody(response)
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return failure(502, "web_upload_blob_failed")
	}
	return nil
}

// processUpload has the file service finish the file and waits until it reports
// the file ready; a message that referenced an unfinished file would dangle.
func (client *webClient) processUpload(ctx context.Context, fileID string, attachment webAttachment) error {
	path := "/backend-api/files/process_upload_stream"
	request, err := json.Marshal(map[string]any{
		"file_id":             fileID,
		"use_case":            webUploadUseCase,
		"index_for_retrieval": false,
		"file_name":           attachment.Name,
		"upload_source":       webUploadSource,
		"entry_surface":       "chat_composer",
	})
	if err != nil {
		return failure(500, "web_request_encoding_failed")
	}
	response, err := client.send(ctx, http.MethodPost, path,
		client.header(path, map[string]string{"Content-Type": "application/json", "Accept": "*/*"}), request)
	if err != nil {
		return err
	}
	defer closeBody(response)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return failure(502, "web_upload_process_failed")
	}
	return webReadProcessing(ctx, response.Body)
}

func webReadProcessing(ctx context.Context, body io.Reader) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), imagesMaxEventBytes)
	ready := false
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(scanner.Text()), "data:"))
		var event struct {
			Event string `json:"event"`
		}
		if !strings.HasPrefix(line, "{") || json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		switch {
		case event.Event == "file.processing.completed":
			return nil
		case event.Event == "file.processing.file_ready":
			ready = true
		case strings.HasPrefix(event.Event, "file.processing."):
			if suffix := event.Event[strings.LastIndex(event.Event, ".")+1:]; suffix == "error" || suffix == "failed" || suffix == "cancelled" || suffix == "unknown" {
				return failure(422, "web_upload_processing_failed")
			}
		}
	}
	if ctx.Err() != nil {
		return failure(499, "web_client_disconnected")
	}
	if ready {
		return nil
	}
	return failure(502, "web_upload_processing_incomplete")
}

// webAttachmentMetadata is the message metadata the app attaches for an uploaded
// file; the conversation resolves each id to the finished upload.
func webAttachmentMetadata(files []webUploadedFile) []map[string]any {
	attachments := make([]map[string]any, 0, len(files))
	for _, file := range files {
		attachments = append(attachments, map[string]any{
			"id":           file.ID,
			"size":         file.Size,
			"name":         file.Name,
			"mime_type":    file.MIME,
			"source":       "local",
			"is_big_paste": false,
		})
	}
	return attachments
}
