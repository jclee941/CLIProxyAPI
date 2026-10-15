package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
)

var (
	imageOriginal = []byte("\x89PNG\r\n\x1a\nfixture-original")
	imagePreview  = []byte("\xff\xd8\xff\xe0fixture-preview")
)

type imageWebFixture struct {
	mu           sync.Mutex
	denyOriginal bool
	noImage      bool
	sparse       bool
	deleteChats  bool
	prompts      []string
	selections   []string
	downloads    []string
	deletes      []string
	// thinking is slot 17 of each turn, as JSON.
	thinking []string
}

// imageWebServer stands in for the web product on an image turn: the bootstrap
// page, the capability and original-download RPCs, the generation stream, and
// the hosts that answer the original with two links before the file.
func imageWebServer(t *testing.T, fixture *imageWebFixture) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := server.URL
		switch {
		case strings.HasSuffix(request.URL.Path, "/app"):
			writeFixture(t, writer, `{"SNlM0e":"xsrf","cfb2h":"build","FdrFJe":"sid"}`)
		case strings.HasSuffix(request.URL.Path, "/batchexecute") && nativeRPCID(request) == accountCapabilityRPC:
			writeCapabilities(t, writer, "3.5 Flash-Lite", "3.8 Flash", "3.1 Pro")
		case strings.HasSuffix(request.URL.Path, "/batchexecute") && nativeRPCID(request) == webDownloadImageRPC:
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				t.Error(err)
			}
			form, _ := url.ParseQuery(string(raw))
			fixture.mu.Lock()
			fixture.downloads = append(fixture.downloads, form.Get("f.req"))
			deny := fixture.denyOriginal
			fixture.mu.Unlock()
			if deny {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			writeFixture(t, writer, rpcEnvelope(t, webDownloadImageRPC, []any{origin + "/original/one"}))
		case strings.HasSuffix(request.URL.Path, "/batchexecute") && nativeRPCID(request) == webDeleteConversationRPC:
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				t.Error(err)
			}
			fixture.mu.Lock()
			fixture.deletes = append(fixture.deletes, string(raw))
			fixture.mu.Unlock()
			writeFixture(t, writer, rpcEnvelope(t, webDeleteConversationRPC, []any{}))
		case strings.HasSuffix(request.URL.Path, "/StreamGenerate"):
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				t.Error(err)
			}
			form, _ := url.ParseQuery(string(raw))
			var envelope []any
			var fields []any
			if json.Unmarshal([]byte(form.Get("f.req")), &envelope) == nil {
				inner, _ := jsonField(envelope, 1).(string)
				_ = json.Unmarshal([]byte(inner), &fields)
			}
			prompt, _ := jsonField(fields, 0, 0).(string)
			fixture.mu.Lock()
			fixture.prompts = append(fixture.prompts, prompt)
			fixture.selections = append(fixture.selections, request.Header.Get("x-goog-ext-525001261-jspb"))
			thinking, _ := json.Marshal(jsonField(fields, 17))
			fixture.thinking = append(fixture.thinking, string(thinking))
			noImage, sparse := fixture.noImage, fixture.sparse
			fixture.mu.Unlock()
			frame := imageFrame(t, origin, sparse)
			if noImage {
				frame = generationFrame(t, "I can't make images right now")
			}
			writeFixture(t, writer, ")]}'\n"+frame+"\n")
		case request.URL.Path == "/original/one=d-I" && request.URL.Query().Get("alr") == "yes":
			writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
			writeFixture(t, writer, origin+"/hop/one")
		case request.URL.Path == "/hop/one":
			writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
			writeFixture(t, writer, origin+"/file/one")
		case request.URL.Path == "/file/one":
			writer.Header().Set("Content-Type", "image/png")
			writeFixture(t, writer, string(imageOriginal))
		case request.URL.Path == "/preview/one=s2048-rj":
			writer.Header().Set("Content-Type", "image/jpeg")
			writeFixture(t, writer, string(imagePreview))
		default:
			t.Errorf("unexpected request %s", request.URL.String())
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// imageFrame is a reply that made one image: a placeholder link in the text and
// the image in field 7 of the rich content, positional or in the sparse bundle.
func imageFrame(t *testing.T, origin string, sparse bool) string {
	t.Helper()
	entries := []any{[]any{[]any{nil, nil, nil, []any{nil, nil, "an apple", origin + "/preview/one"}}, []any{"img-1"}}}
	rich := slots(8, map[int]any{7: []any{entries}})
	if sparse {
		rich = []any{nil, map[string]any{"8": []any{entries}}}
	}
	candidate := slots(13, map[int]any{0: "rc_img", 1: []any{"Here is your apple.\n\nhttp://googleusercontent.com/image_generation_content/0\n"}, 12: rich})
	inner, err := json.Marshal(slots(26, map[int]any{1: []any{"c_img", "r_img"}, 4: []any{candidate}}))
	if err != nil {
		t.Fatal(err)
	}
	frame, err := json.Marshal([]any{[]any{"wrb.fr", nil, string(inner)}})
	if err != nil {
		t.Fatal(err)
	}
	return string(frame)
}

func imageCall(t *testing.T, fixture *imageWebFixture, model string) envelope {
	t.Helper()
	server := imageWebServer(t, fixture)
	service, record := nativeService(t, server)
	service.client = server.Client()
	service.config.DeleteChats = fixture.deleteChats
	return invoke(t, service, "executor.execute", executorRequest{
		AuthID: record.ID, AuthProvider: provider, Model: model,
		Format: "gemini", SourceFormat: "gemini", StorageJSON: jsonFixture(t, record),
		Payload: []byte(`{"contents":[{"role":"user","parts":[{"text":"a red apple"}]}]}`),
	})
}

type imagePart struct {
	Text       string `json:"text"`
	InlineData struct {
		MIMEType string `json:"mimeType"`
		Data     string `json:"data"`
	} `json:"inlineData"`
}

func imageParts(t *testing.T, result envelope) []imagePart {
	t.Helper()
	if !result.OK {
		t.Fatalf("image turn failed: %+v", result.Error)
	}
	var response struct{ Payload []byte }
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Candidates []struct {
			Content struct {
				Parts []imagePart `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(response.Payload, &body); err != nil || len(body.Candidates) != 1 {
		t.Fatalf("image response = %s", response.Payload)
	}
	return body.Candidates[0].Content.Parts
}

// Image generation is not published; the per-mode image list stays as it was
// for when it is offered again.
func TestImageModelsAreNotPublished(t *testing.T) {
	service := newService(nil)
	ids := func(models []modelInfo) []string {
		var ids []string
		for _, model := range models {
			ids = append(ids, model.ID)
		}
		return ids
	}
	full := accountModels{Available: true, Models: []capability{{DisplayName: "3.5 Flash-Lite"}, {DisplayName: "3.8 Flash"}, {DisplayName: "3.1 Pro"}}}
	if published := ids(service.interactionModels(full)); !slices.Equal(published, []string{interactionOmniModel, "gemini-app-3.5-flash-lite", "gemini-app-3.8-flash", "gemini-app-3.1-pro", webMusicModel, webResearchModel}) {
		t.Fatalf("published = %v", published)
	}
	if images := ids(webImageModelInfos(full)); !slices.Equal(images, []string{"gemini-nano-banana-2", "gemini-nano-banana-pro"}) {
		t.Fatalf("image models for the modes = %v", images)
	}
	if lite := ids(webImageModelInfos(accountModels{Available: true, Models: []capability{{DisplayName: "3.5 Flash-Lite"}}})); len(lite) != 0 {
		t.Fatalf("Flash-Lite is not the Flash mode, image models = %v", lite)
	}
}

func TestImageTurnReturnsTheOriginalInline(t *testing.T) {
	fixture := &imageWebFixture{}
	parts := imageParts(t, imageCall(t, fixture, "gemini-nano-banana-pro"))
	if len(parts) != 2 || parts[0].Text != "Here is your apple." {
		t.Fatalf("parts = %+v", parts)
	}
	if parts[1].InlineData.MIMEType != "image/png" || parts[1].InlineData.Data != base64.StdEncoding.EncodeToString(imageOriginal) {
		t.Fatalf("image part = %+v", parts[1])
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if !slices.Equal(fixture.prompts, []string{"Generate an image: a red apple"}) {
		t.Fatalf("prompts = %q", fixture.prompts)
	}
	var selection []any
	if len(fixture.selections) != 1 || json.Unmarshal([]byte(fixture.selections[0]), &selection) != nil || jsonField(selection, 4) != "cap-2" {
		t.Fatalf("the turn did not run in the Pro mode: %v", fixture.selections)
	}
	if len(fixture.downloads) != 1 {
		t.Fatalf("original downloads = %d", len(fixture.downloads))
	}
	for _, id := range []string{"img-1", "r_img", "rc_img", "c_img"} {
		if !strings.Contains(fixture.downloads[0], id) {
			t.Fatalf("the original download does not name %s: %s", id, fixture.downloads[0])
		}
	}
}

func TestImageTurnFallsBackToTheLargestPreview(t *testing.T) {
	fixture := &imageWebFixture{denyOriginal: true, sparse: true}
	parts := imageParts(t, imageCall(t, fixture, "gemini-nano-banana-2"))
	if len(parts) != 2 || parts[1].InlineData.MIMEType != "image/jpeg" || parts[1].InlineData.Data != base64.StdEncoding.EncodeToString(imagePreview) {
		t.Fatalf("parts = %+v", parts)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	var selection []any
	if len(fixture.selections) != 1 || json.Unmarshal([]byte(fixture.selections[0]), &selection) != nil || jsonField(selection, 4) != "cap-flash" {
		t.Fatalf("the turn did not run in the Flash mode: %v", fixture.selections)
	}
}

// An operator who keeps generated chats out of the account history has the
// conversation a text or image turn opened deleted once it answered.
func TestGeneratedChatsAreDeletedOnlyWhenConfigured(t *testing.T) {
	for _, model := range []string{"gemini-nano-banana-2", "gemini-app-3.8-flash"} {
		kept := &imageWebFixture{}
		if result := imageCall(t, kept, model); !result.OK {
			t.Fatalf("%s: %+v", model, result.Error)
		}
		deleted := &imageWebFixture{deleteChats: true}
		if result := imageCall(t, deleted, model); !result.OK {
			t.Fatalf("%s with deletion: %+v", model, result.Error)
		}
		kept.mu.Lock()
		keptDeletes := len(kept.deletes)
		kept.mu.Unlock()
		deleted.mu.Lock()
		deletes := slices.Clone(deleted.deletes)
		deleted.mu.Unlock()
		if keptDeletes != 0 || len(deletes) != 1 || !strings.Contains(deletes[0], "c_img") {
			t.Fatalf("%s: deletes without the setting %d, with it %q", model, keptDeletes, deletes)
		}
	}
}

// A text turn sends slot 17 as the web app does; 3.1 Pro refused every turn
// that carried the old MEDIUM there.
func TestTextTurnSendsTheWebAppsThinkingSlot(t *testing.T) {
	fixture := &imageWebFixture{}
	if result := imageCall(t, fixture, "gemini-app-3.8-flash"); !result.OK {
		t.Fatalf("text turn failed: %+v", result.Error)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if !slices.Equal(fixture.thinking, []string{"[[0]]"}) {
		t.Fatalf("slot 17 = %q, want the web app's [[0]]", fixture.thinking)
	}
}

func TestImageTurnWithoutAnImageIsRefused(t *testing.T) {
	result := imageCall(t, &imageWebFixture{noImage: true}, "gemini-nano-banana-2")
	if result.OK || result.Error == nil || result.Error.Code != "no_image_generated" || !strings.Contains(result.Error.Message, "I can't make images right now") {
		t.Fatalf("result = %+v", result)
	}
}
