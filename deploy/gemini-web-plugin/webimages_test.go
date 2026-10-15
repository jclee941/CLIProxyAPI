package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/textproto"
	"slices"
	"strings"
	"testing"
)

func TestImagesEndpointsAnswerWithTheImageTurnsImages(t *testing.T) {
	fixture := &imageWebFixture{}
	server := imageWebServer(t, fixture)
	service, record := nativeService(t, server)
	service.client = server.Client()

	result := invoke(t, service, "executor.execute", executorRequest{
		AuthID: record.ID, AuthProvider: provider, Model: "gemini-nano-banana-2-image",
		Format: webImagesFormat, SourceFormat: webImagesFormat, StorageJSON: jsonFixture(t, record),
		Payload: []byte(`{"model":"gemini-nano-banana-2-image","prompt":"a red fox","n":1,"size":"1024x1024","response_format":"b64_json"}`),
	})

	if !result.OK {
		t.Fatalf("images request failed: %+v", result.Error)
	}
	var response struct{ Payload []byte }
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Created int64 `json:"created"`
		Data    []struct {
			B64JSON  string `json:"b64_json"`
			MIMEType string `json:"mime_type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Payload, &body); err != nil || body.Created == 0 || len(body.Data) == 0 {
		t.Fatalf("images response %s: %v", response.Payload, err)
	}
	for _, image := range body.Data {
		if _, err := base64.StdEncoding.DecodeString(image.B64JSON); err != nil || image.B64JSON == "" || !strings.HasPrefix(image.MIMEType, "image/") {
			t.Fatalf("image = %.40q %q", image.B64JSON, image.MIMEType)
		}
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if !slices.Equal(fixture.prompts, []string{webImagePrompt("a red fox")}) {
		t.Fatalf("prompts = %q", fixture.prompts)
	}
}

func imagesEditForm(t *testing.T, fields map[string]string, image []byte) []byte {
	t.Helper()
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if image != nil {
		part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="image"; filename="in.png"`}, "Content-Type": {"image/png"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(image); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return form.Bytes()
}

// An edit arrives as the multipart form the host forwards, with no header to
// name its boundary; the image it carries goes to the turn beside the prompt.
func TestImagesEditFormCarriesItsImageAndRefusesWhatTheWebCannotDo(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nfixture")
	prompt, media, err := webImagesRequest(imagesEditForm(t, map[string]string{"prompt": "make it blue", "model": "gemini-nano-banana-2-image"}, png))
	if err != nil || prompt != "make it blue" || len(media) != 1 || media[0].MIMEType != "image/png" || media[0].Data != base64.StdEncoding.EncodeToString(png) {
		t.Fatalf("edit = %q %+v %v", prompt, media, err)
	}
	for name, form := range map[string][]byte{
		"mask":      imagesEditForm(t, map[string]string{"prompt": "x", "mask": "m"}, png),
		"n":         imagesEditForm(t, map[string]string{"prompt": "x", "n": "2"}, png),
		"no prompt": imagesEditForm(t, map[string]string{"model": "gemini-nano-banana-2-image"}, png),
		"json n":    []byte(`{"prompt":"x","n":4}`),
	} {
		if _, _, err := webImagesRequest(form); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}
