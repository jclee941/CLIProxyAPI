package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type interactionFilesTransport struct {
	web   http.RoundTripper
	drive string
}

func (transport interactionFilesTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if strings.HasPrefix(request.URL.String(), transport.drive+"/") {
		return http.DefaultTransport.RoundTrip(request)
	}
	return transport.web.RoundTrip(request)
}

func interactionFilesFixture(t *testing.T) (*service, localSession, *continuationWebFixture) {
	t.Helper()
	service, local := continuationFixture(t)
	web := &continuationWebFixture{video: true}
	continuationWeb(t, service, web)
	drive := newFilesDriveFixture(t)
	drive.attach(service)
	service.client.Transport = interactionFilesTransport{web: service.client.Transport, drive: drive.server.URL}
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	return service, local, web
}

func TestInteractionFilesReferenceUsesCallerOwnedDriveBytes(t *testing.T) {
	service, local, web := interactionFilesFixture(t)
	content := []byte("0000ftypstored-source")
	file, err := service.saveGeneratedFile(t.Context(), testCallerScope, "previous-video", "video/mp4", content)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"model":"gemini-omni-1.1-flash","input":[{"type":"user_input","content":[{"type":"text","text":"Continue the scene"},{"type":"video","uri":"` + file.URI + `","mime_type":"video/mp4"}]}],"generation_config":{"video_config":{"task":"extend"}}}`
	interactionID(t, interactionCall(t, service, local, body))
	web.mu.Lock()
	defer web.mu.Unlock()
	if len(web.uploads) != 1 || !bytes.Equal(web.uploads[0], content) {
		t.Fatalf("Files bytes did not reach the selected account: %d uploads", len(web.uploads))
	}
}

func TestInteractionRejectsForeignFilesBeforeSubmission(t *testing.T) {
	service, local, web := interactionFilesFixture(t)
	file, err := service.saveGeneratedFile(t.Context(), testCallerScope, "private-source", "video/mp4", []byte("0000ftypprivate"))
	if err != nil {
		t.Fatal(err)
	}
	request := interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","input":[{"type":"text","text":"Use this video"},{"type":"video","uri":"`+file.URI+`"}]}`)
	request.Metadata.CallerScope = strings.Repeat("d", 64)
	if _, err := service.executeInteraction(t.Context(), request); safeCredentialCode(err) != "file_not_found" {
		t.Fatalf("foreign file was not rejected: %v", err)
	}
	web.mu.Lock()
	defer web.mu.Unlock()
	if len(web.uploads) != 0 || len(web.fields) != 0 {
		t.Fatal("foreign file caused an upstream upload or generation")
	}
}

func TestURIStoragePermissionIsCheckedBeforeVideoGeneration(t *testing.T) {
	service, local := continuationFixture(t)
	web := &continuationWebFixture{video: true}
	continuationWeb(t, service, web)
	drive := newFilesDriveFixture(t)
	drive.readOnly, drive.denyWrites = true, true
	drive.attach(service)
	service.client.Transport = interactionFilesTransport{web: service.client.Transport, drive: drive.server.URL}
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	request := interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","input":"A paper boat","response_format":{"type":"video","delivery":"uri"}}`)
	if _, err := service.executeInteraction(t.Context(), request); safeCredentialCode(err) != "files_drive_write_permission_required" {
		t.Fatalf("storage permission error = %v", err)
	}
	web.mu.Lock()
	defer web.mu.Unlock()
	if len(web.fields) != 0 {
		t.Fatal("video quota was spent despite known read-only output storage")
	}
}

func TestInteractionURIDeliveryStoresDownloadableCallerOwnedVideo(t *testing.T) {
	service, local, _ := interactionFilesFixture(t)
	call := interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"A paper boat","response_format":{"type":"video","delivery":"uri"}}`)
	if !call.OK {
		t.Fatalf("URI creation failed: %v", call.Error)
	}
	var result continuationResult
	if err := json.Unmarshal(call.Result, &result); err != nil {
		t.Fatal(err)
	}
	var body struct {
		ID    string
		Steps []struct {
			Content []struct{ Type, URI, Data string }
		}
	}
	if err := json.Unmarshal(result.Payload, &body); err != nil {
		t.Fatal(err)
	}
	id := body.ID
	if len(id) != 64 || len(body.Steps) != 1 || len(body.Steps[0].Content) != 1 {
		t.Fatalf("invalid URI interaction response: %s", result.Payload)
	}
	video := body.Steps[0].Content[0]
	if video.Type != "video" || video.URI == "" || video.Data != "" {
		t.Fatalf("URI delivery was not honored: %+v", video)
	}
	source, err := service.resolveFileSource(testCallerScope, video.URI)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := source.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(reader)
	if closeErr := reader.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil || string(content) != "0000ftypvideo" {
		t.Fatalf("stored video differs: %q %v", content, err)
	}
	if _, err := service.resolveFileSource(strings.Repeat("d", 64), video.URI); err == nil {
		t.Fatal("another caller could resolve the generated file")
	}
	get := interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","id":"`+id+`"}`)
	get.Alt = interactionRetrieveAlt
	retrieved, err := service.executeInteraction(t.Context(), get)
	if err != nil || !strings.Contains(string(retrieved.(continuationResult).Payload), `"data":`) {
		t.Fatalf("GET must retain the documented inline video: %v", err)
	}
}
