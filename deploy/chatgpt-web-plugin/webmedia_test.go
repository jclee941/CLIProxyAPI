package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// mediaFake plays the ChatGPT web backend at the HTTP boundary: the file
// service (create, blob upload, processing) and the conversation endpoints. It
// records what the plugin sent so a test can assert the audio bytes, the
// finalized file references and the message wire rather than prose.
type mediaFake struct {
	mu            sync.Mutex
	events        []string
	creates       []map[string]any
	blobs         map[string][]byte
	blobHeaders   map[string]http.Header
	processed     []string
	conversation  map[string]any
	rejectCreate  bool
	blobStatus    int
	onBlob        func()
	uploadHeaders map[string]string
}

func newMediaFake() *mediaFake {
	return &mediaFake{blobs: map[string][]byte{}, blobHeaders: map[string]http.Header{}, blobStatus: http.StatusCreated}
}

func (fake *mediaFake) RoundTrip(request *http.Request) (*http.Response, error) {
	var body []byte
	if request.Body != nil {
		body, _ = io.ReadAll(request.Body)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	path := request.URL.Path
	fake.events = append(fake.events, request.Method+" "+path)
	reply := func(status int, text string) (*http.Response, error) {
		return textResponse(request, status, text), nil
	}
	if request.URL.Host == "blob.oaiusercontent.com" {
		if fake.onBlob != nil {
			fake.onBlob()
			return nil, request.Context().Err()
		}
		fake.blobs[path] = body
		fake.blobHeaders[path] = request.Header.Clone()
		return reply(fake.blobStatus, "")
	}
	switch path {
	case "/":
		return reply(200, "<html></html>")
	case "/backend-api/sentinel/chat-requirements/prepare":
		return reply(200, `{"prepare_token":"pt","proofofwork":{"required":false}}`)
	case "/backend-api/sentinel/chat-requirements/finalize":
		return reply(200, `{"token":"rt"}`)
	case "/backend-api/files":
		var create map[string]any
		_ = json.Unmarshal(body, &create)
		fake.creates = append(fake.creates, create)
		if fake.rejectCreate {
			return reply(200, `{"status":"error","error_code":"unsupported_mime"}`)
		}
		id := fmt.Sprintf("file_%02d", len(fake.creates))
		grant, _ := json.Marshal(fake.uploadHeaders)
		return reply(200, fmt.Sprintf(`{"status":"success","file_id":%q,"upload_url":"https://blob.oaiusercontent.com/up/%s?sig=secret","upload_headers":%s}`, id, id, grant))
	case "/backend-api/files/process_upload_stream":
		var process struct {
			FileID string `json:"file_id"`
		}
		_ = json.Unmarshal(body, &process)
		fake.processed = append(fake.processed, process.FileID)
		return reply(200, fmt.Sprintf("data: {\"event\":\"file.processing.started\",\"file_id\":%q}\n\ndata: {\"event\":\"file.processing.completed\",\"file_id\":%q}\n\n", process.FileID, process.FileID))
	case "/backend-api/f/conversation/prepare":
		return reply(200, `{"conduit_token":"ct"}`)
	case "/backend-api/f/conversation":
		_ = json.Unmarshal(body, &fake.conversation)
		return reply(200, "data: [DONE]\n")
	}
	return reply(404, "")
}

// waveBytes is a valid PCM WAV header followed by a recognizable payload, so two
// clips differ in their sample bytes and not only in their names.
func waveBytes(fill byte, samples int) []byte {
	data := bytes.Repeat([]byte{fill, fill ^ 0x5a}, samples)
	header := make([]byte, 44)
	copy(header[0:], "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(36+len(data)))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], 1)
	binary.LittleEndian.PutUint32(header[24:], 16000)
	binary.LittleEndian.PutUint32(header[28:], 32000)
	binary.LittleEndian.PutUint16(header[32:], 2)
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(len(data)))
	return append(header, data...)
}

func mp3Bytes(fill byte) []byte {
	return append([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"), bytes.Repeat([]byte{0xff, 0xfb, fill}, 64)...)
}

func mp4Bytes(fill byte) []byte {
	return append([]byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2"), bytes.Repeat([]byte{fill}, 64)...)
}

func dataURL(mime string, raw []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)
}

func filePart(name, mime string, raw []byte) string {
	return fmt.Sprintf(`{"type":"file","file":{"filename":%q,"file_data":%q}}`, name, dataURL(mime, raw))
}

func userTurn(parts ...string) []byte {
	return []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"Compare the narrations."},` + strings.Join(parts, ",") + `]}]}`)
}

// mediaPlan runs a chat payload through the same planning step the host calls and
// returns the turn that would be sent.
func mediaPlan(t *testing.T, payload []byte) (webChatPlan, error) {
	t.Helper()
	recorder := &hostRecorder{}
	raw, err := json.Marshal(executorRequest{Model: "gpt-app-pro", Payload: payload, HostCallbackID: "cb"})
	if err != nil {
		t.Fatal(err)
	}
	return newService(recorder.call).planChat(raw)
}

func runMedia(t *testing.T, ctx context.Context, fake *mediaFake, payload []byte) error {
	t.Helper()
	plan, err := mediaPlan(t, payload)
	if err != nil {
		return err
	}
	client := clientWith(t, fake.RoundTrip)
	response, err := client.startReply(ctx, plan.turn)
	if err == nil {
		closeBody(response)
	}
	return err
}

func attachmentIDs(t *testing.T, fake *mediaFake) []map[string]any {
	t.Helper()
	messages, _ := fake.conversation["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("conversation messages = %v", fake.conversation["messages"])
	}
	message := messages[0].(map[string]any)
	metadata := message["metadata"].(map[string]any)
	var attachments []map[string]any
	for _, entry := range metadata["attachments"].([]any) {
		attachments = append(attachments, entry.(map[string]any))
	}
	return attachments
}

// Both clips have to travel as real bytes through create, blob upload and
// processing, and the conversation message has to reference the finalized files in
// the order the caller gave them: the A/B comparison is meaningless otherwise.
func TestWebChatUploadsEachAudioFileAndReferencesThemInOrder(t *testing.T) {
	clipA, clipB := waveBytes(0x11, 4000), waveBytes(0x77, 4000)
	for name, order := range map[string][][2]any{
		"A then B": {{"A.wav", clipA}, {"B.wav", clipB}},
		"B then A": {{"B.wav", clipB}, {"A.wav", clipA}},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newMediaFake()
			var parts []string
			for _, clip := range order {
				parts = append(parts, filePart(clip[0].(string), "audio/wav", clip[1].([]byte)))
			}
			if err := runMedia(t, t.Context(), fake, userTurn(parts...)); err != nil {
				t.Fatalf("turn failed: %v", err)
			}
			if len(fake.creates) != 2 {
				t.Fatalf("file entries = %d", len(fake.creates))
			}
			attachments := attachmentIDs(t, fake)
			for index, clip := range order {
				create := fake.creates[index]
				raw := clip[1].([]byte)
				if create["file_name"] != clip[0] || int(create["file_size"].(float64)) != len(raw) || create["mime_type"] != "audio/wav" || create["use_case"] != "multimodal" {
					t.Fatalf("create %d = %v", index, create)
				}
				id := fmt.Sprintf("file_%02d", index+1)
				if got := fake.blobs["/up/"+id]; !bytes.Equal(got, raw) {
					t.Fatalf("blob %s did not carry clip %v: %d bytes", id, clip[0], len(got))
				}
				header := fake.blobHeaders["/up/"+id]
				if header.Get("x-ms-blob-type") != "BlockBlob" || header.Get("Content-Type") != "audio/wav" {
					t.Fatalf("blob headers = %v", header)
				}
				if attachments[index]["id"] != id || attachments[index]["name"] != clip[0] || attachments[index]["mime_type"] != "audio/wav" || int(attachments[index]["size"].(float64)) != len(raw) {
					t.Fatalf("attachment %d = %v", index, attachments[index])
				}
			}
			if strings.Join(fake.processed, ",") != "file_01,file_02" {
				t.Fatalf("processed = %v", fake.processed)
			}
			if bytes.Equal(fake.blobs["/up/file_01"], fake.blobs["/up/file_02"]) {
				t.Fatal("the two clips reached the backend as the same bytes")
			}
		})
	}
}

// The conversation must only start once every file is finalized, still ask for the
// Pro slug, and keep the prompt as the text part; a file reference without a
// finished upload would be a dangling pointer.
func TestWebChatStartsTheConversationAfterTheUploadsAndKeepsTheProModel(t *testing.T) {
	fake := newMediaFake()
	if err := runMedia(t, t.Context(), fake, userTurn(filePart("clip.mp3", "audio/mpeg", mp3Bytes(1)), filePart("scene.mp4", "video/mp4", mp4Bytes(2)))); err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	last := func(prefix string) int {
		found := -1
		for index, event := range fake.events {
			if strings.HasPrefix(event, prefix) {
				found = index
			}
		}
		return found
	}
	if last("POST /backend-api/files/process_upload_stream") > last("POST /backend-api/f/conversation/prepare") || last("PUT /up/") > last("POST /backend-api/files/process_upload_stream") {
		t.Fatalf("upload steps ran out of order: %v", fake.events)
	}
	if fake.conversation["model"] != "gpt-6-pro" {
		t.Fatalf("model = %v", fake.conversation["model"])
	}
	message := fake.conversation["messages"].([]any)[0].(map[string]any)
	content := message["content"].(map[string]any)
	if content["content_type"] != "text" || !strings.Contains(fmt.Sprint(content["parts"]), "Compare the narrations.") {
		t.Fatalf("content = %v", content)
	}
	attachments := attachmentIDs(t, fake)
	if attachments[0]["mime_type"] != "audio/mpeg" || attachments[1]["mime_type"] != "video/mp4" || attachments[1]["name"] != "scene.mp4" {
		t.Fatalf("attachments = %v", attachments)
	}
}

// input_audio carries no file name, so the plugin names the clip from its format and
// position, and the audio still has to reach the backend as the decoded bytes.
func TestWebChatNamesInputAudioByPosition(t *testing.T) {
	fake := newMediaFake()
	clip := waveBytes(0x33, 800)
	part := fmt.Sprintf(`{"type":"input_audio","input_audio":{"data":%q,"format":"wav"}}`, base64.StdEncoding.EncodeToString(clip))
	if err := runMedia(t, t.Context(), fake, userTurn(part)); err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	if fake.creates[0]["file_name"] != "audio-1.wav" || !bytes.Equal(fake.blobs["/up/file_01"], clip) {
		t.Fatalf("create = %v", fake.creates[0])
	}
}

// Anything the app cannot be shown to use stays an explicit refusal, and bytes that
// do not match their declared type never reach the network.
func TestWebChatValidatesAttachmentsAtTheInputBoundary(t *testing.T) {
	cases := map[string]struct {
		payload []byte
		code    string
		status  int
	}{
		"pdf is not a supported media type": {userTurn(filePart("a.pdf", "application/pdf", []byte("%PDF-1.4"))), "unsupported_chat_attachment", 400},
		"image stays unsupported":           {userTurn(`{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}`), "unsupported_chat_attachment", 400},
		"wav bytes that are not RIFF":       {userTurn(filePart("a.wav", "audio/wav", []byte("not a wave file at all"))), "invalid_chat_attachment", 400},
		"mp4 declared with wav bytes":       {userTurn(filePart("a.mp4", "video/mp4", waveBytes(1, 10))), "invalid_chat_attachment", 400},
		"empty file":                        {userTurn(filePart("a.wav", "audio/wav", nil)), "invalid_chat_attachment", 400},
		"bad base64":                        {userTurn(`{"type":"file","file":{"filename":"a.wav","file_data":"data:audio/wav;base64,@@@@"}}`), "invalid_chat_attachment", 400},
		"file id without bytes":             {userTurn(`{"type":"file","file":{"file_id":"file-abc"}}`), "unsupported_chat_attachment", 400},
		"remote url is not fetched":         {userTurn(`{"type":"video_url","video_url":{"url":"https://example.com/a.mp4"}}`), "unsupported_chat_attachment", 400},
		"media in an assistant turn":        {[]byte(`{"messages":[{"role":"assistant","content":[` + filePart("a.wav", "audio/wav", waveBytes(1, 10)) + `]},{"role":"user","content":"hi"}]}`), "unsupported_chat_attachment", 400},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := mediaPlan(t, test.payload)
			var public *publicError
			if !errors.As(err, &public) || public.HTTPStatus != test.status || public.Code != test.code {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestWebChatBoundsAttachmentSizeAndCount(t *testing.T) {
	previous := webAttachmentMaxBytes
	webAttachmentMaxBytes = 1024
	t.Cleanup(func() { webAttachmentMaxBytes = previous })
	_, err := mediaPlan(t, userTurn(filePart("big.wav", "audio/wav", waveBytes(1, 2000))))
	var public *publicError
	if !errors.As(err, &public) || public.HTTPStatus != http.StatusRequestEntityTooLarge || public.Code != "chat_attachment_too_large" {
		t.Fatalf("oversize err = %v", err)
	}
	webAttachmentMaxBytes = previous
	var parts []string
	for index := 0; index <= webAttachmentMaxFiles; index++ {
		parts = append(parts, filePart(fmt.Sprintf("%d.wav", index), "audio/wav", waveBytes(byte(index), 8)))
	}
	if _, err := mediaPlan(t, userTurn(parts...)); !errors.As(err, &public) || public.Code != "too_many_chat_attachments" {
		t.Fatalf("count err = %v", err)
	}
}

// A refused or failed upload must stop the turn before any conversation is opened.
func TestWebChatStopsBeforeTheConversationWhenAnUploadFails(t *testing.T) {
	payload := userTurn(filePart("A.wav", "audio/wav", waveBytes(1, 100)))
	for name, arrange := range map[string]func(*mediaFake){
		"create refused": func(fake *mediaFake) { fake.rejectCreate = true },
		"blob refused":   func(fake *mediaFake) { fake.blobStatus = http.StatusForbidden },
	} {
		t.Run(name, func(t *testing.T) {
			fake := newMediaFake()
			arrange(fake)
			err := runMedia(t, t.Context(), fake, payload)
			var public *publicError
			if !errors.As(err, &public) || public.HTTPStatus < 400 {
				t.Fatalf("err = %v", err)
			}
			if len(fake.creates) != 1 || fake.conversation != nil || len(fake.processed) != 0 {
				t.Fatalf("the turn went on after a failed upload: %v", fake.events)
			}
			if strings.Contains(public.Message, "sig=secret") {
				t.Fatalf("the signed URL leaked: %s", public.Message)
			}
		})
	}
}

func TestWebChatCancellationStopsTheUploadBeforeProcessing(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	fake := newMediaFake()
	fake.onBlob = cancel
	err := runMedia(t, ctx, fake, userTurn(filePart("A.wav", "audio/wav", waveBytes(1, 100))))
	if err == nil {
		t.Fatal("a cancelled upload reported success")
	}
	if len(fake.creates) != 1 || len(fake.processed) != 0 || fake.conversation != nil {
		t.Fatalf("work continued after cancellation: %v", fake.events)
	}
}

// The signed blob target is a different party from the ChatGPT backend: the
// bearer token and the session cookies the client holds belong to chatgpt.com
// only, whatever headers the file service grants for the upload.
func TestWebChatBlobUploadNeverCarriesChatGPTCredentials(t *testing.T) {
	fake := newMediaFake()
	fake.uploadHeaders = map[string]string{"Authorization": "Bearer grant", "Cookie": "grant=1", "x-ms-meta-kind": "audio"}
	plan, err := mediaPlan(t, userTurn(filePart("a.wav", "audio/wav", waveBytes(0x11, 100))))
	if err != nil {
		t.Fatal(err)
	}
	client := clientWith(t, fake.RoundTrip)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(&url.URL{Scheme: "https", Host: "blob.oaiusercontent.com"}, []*http.Cookie{{Name: "session", Value: "secret", Path: "/"}})
	client.http.Jar = jar
	response, err := client.startReply(context.Background(), plan.turn)
	if err != nil {
		t.Fatal(err)
	}
	closeBody(response)
	header := fake.blobHeaders["/up/file_01"]
	if header == nil {
		t.Fatal("the blob was never uploaded")
	}
	if header.Get("Authorization") != "" || header.Get("Cookie") != "" {
		t.Fatalf("blob upload carried credentials: %v", header)
	}
	if header.Get("x-ms-meta-kind") != "audio" {
		t.Fatalf("blob upload lost the granted header: %v", header)
	}
}

// Only the Pro model advertises media input: the attachments were verified on it,
// and the auto-routed chat model keeps its text-only declaration.
func TestWebChatCatalogDeclaresMediaInputOnlyForPro(t *testing.T) {
	for _, model := range webChatModels() {
		want := []string{"text"}
		if model.ID == webProModel {
			want = []string{"text", "audio", "video"}
		}
		if strings.Join(model.SupportedInputModalities, ",") != strings.Join(want, ",") {
			t.Fatalf("%s input modalities = %v, want %v", model.ID, model.SupportedInputModalities, want)
		}
	}
}
