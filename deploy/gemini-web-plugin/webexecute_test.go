package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// nativeWebServer stands in for the web product: the bootstrap page, the
// capability RPC and the generation stream, each on the path prefix the
// credential's auth_user selects.
var nativeGenerationBody struct {
	mu   sync.Mutex
	body string
}

func nativeLastGeneration(t *testing.T) string {
	t.Helper()
	nativeGenerationBody.mu.Lock()
	defer nativeGenerationBody.mu.Unlock()
	return nativeGenerationBody.body
}

func nativeWebServer(t *testing.T, reply string) *httptest.Server {
	t.Helper()
	capabilities := slots(16, map[int]any{
		14: float64(1000),
		15: []any{slots(18, map[int]any{0: "cap-flash", 11: "3.8 Flash", 17: float64(1)})},
	})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/app"):
			if _, err := writer.Write([]byte(`{"SNlM0e":"xsrf","cfb2h":"build","FdrFJe":"sid"}`)); err != nil {
				t.Error(err)
			}
		case strings.HasSuffix(request.URL.Path, "/batchexecute"):
			if _, err := writer.Write([]byte(rpcEnvelope(t, accountCapabilityRPC, capabilities))); err != nil {
				t.Error(err)
			}
		case strings.HasSuffix(request.URL.Path, "/StreamGenerate"):
			if raw, readErr := io.ReadAll(request.Body); readErr == nil {
				nativeGenerationBody.mu.Lock()
				nativeGenerationBody.body = string(raw)
				nativeGenerationBody.mu.Unlock()
			}
			selection := request.Header.Get("x-goog-ext-525001261-jspb")
			if !strings.Contains(selection, "cap-flash") {
				t.Errorf("selection header did not carry the capability: %s", selection)
			}
			if request.Header.Get("x-goog-ext-73010990-jspb") != "[0,0,0]" {
				t.Errorf("generation header missing: %v", request.Header)
			}
			if _, err := writer.Write([]byte(")]}'\n" + generationFrame(t, reply) + "\n")); err != nil {
				t.Error(err)
			}
		case strings.HasPrefix(request.URL.Path, "/upload/"):
			if request.Header.Get("X-Goog-Upload-Command") == "start" {
				writer.Header().Set("X-Goog-Upload-Url", "http://"+request.Host+"/upload/leg2")
				writer.WriteHeader(http.StatusOK)
				return
			}
			writeFixture(t, writer, "/contrib_service/ttl_1d/fixture-upload")
		default:
			t.Errorf("unexpected path %s", request.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func nativeService(t *testing.T, server *httptest.Server) (*service, storageRecord) {
	t.Helper()
	service := newService(nil)
	record := recordFixture(t, "a")
	seedSessions(t, service, map[string]sessionToken{record.TokenRef: {encodedToken("SID=a; SAPISID=secret")}})
	service.config.NativeGeneration = true
	service.webOriginOverride = server.URL
	service.webUploadOverride = server.URL
	return service, record
}

func TestNativeTextServesFlashWithoutTheSidecar(t *testing.T) {
	server := nativeWebServer(t, "plain answer")
	service, record := nativeService(t, server)
	result := invoke(t, service, "executor.execute", executorRequest{
		AuthID: record.ID, AuthProvider: provider, Model: flashModel,
		Format: "gemini", SourceFormat: "gemini", StorageJSON: jsonFixture(t, record),
		Payload: []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`),
	})
	if !result.OK {
		t.Fatalf("native execute failed: %+v", result.Error)
	}
	var response struct{ Payload []byte }
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Payload, &body); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if text, _ := jsonField(body["candidates"], 0).(map[string]any); text == nil {
		t.Fatalf("no candidate: %s", response.Payload)
	}
	if !strings.Contains(string(response.Payload), "plain answer") {
		t.Fatalf("reply not carried: %s", response.Payload)
	}
	if !strings.Contains(string(response.Payload), flashModel) {
		t.Fatalf("modelVersion not set: %s", response.Payload)
	}
}

// A reply carrying the requested block must reach the caller as a real
// functionCall part, which is what the tool contract promised.
func TestNativeTextConvertsTheToolBlock(t *testing.T) {
	server := nativeWebServer(t, "```function_call\n{\"name\": \"test_tool\", \"args\": {\"city\": \"Seoul\"}}\n```")
	service, record := nativeService(t, server)
	result := invoke(t, service, "executor.execute", executorRequest{
		AuthID: record.ID, AuthProvider: provider, Model: flashModel,
		Format: "gemini", SourceFormat: "gemini", StorageJSON: jsonFixture(t, record),
		Payload: []byte(`{"contents":[{"role":"user","parts":[{"text":"weather?"}]}],"tools":[{"functionDeclarations":[{"name":"test_tool"}]}],"toolConfig":{"functionCallingConfig":{"mode":"ANY"}}}`),
	})
	if !result.OK {
		t.Fatalf("native execute failed: %+v", result.Error)
	}
	var response struct{ Payload []byte }
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	payload := string(response.Payload)
	if !strings.Contains(payload, `"functionCall"`) || !strings.Contains(payload, "test_tool") {
		t.Fatalf("tool call not emitted: %s", payload)
	}
	if !strings.Contains(payload, "Seoul") {
		t.Fatalf("arguments lost: %s", payload)
	}
	if strings.Contains(payload, "function_call\\n") {
		t.Fatalf("raw block leaked into the reply: %s", payload)
	}
}

func TestNativeTextUploadsInlineMediaIntoThePromptSlot(t *testing.T) {
	server := nativeWebServer(t, "a colour")
	service, record := nativeService(t, server)

	result := invoke(t, service, "executor.execute", executorRequest{
		AuthID: record.ID, AuthProvider: provider, Model: flashModel,
		Format: "gemini", SourceFormat: "gemini", StorageJSON: jsonFixture(t, record),
		Payload: []byte(`{"contents":[{"role":"user","parts":[{"text":"look"},{"inlineData":{"mimeType":"image/png","data":"AAAA"}}]}]}`),
	})

	if !result.OK {
		t.Fatalf("an attachment turn was refused: %+v", result.Error)
	}
	form, err := url.ParseQuery(nativeLastGeneration(t))
	if err != nil {
		t.Fatal(err)
	}
	body := form.Get("f.req")
	if !strings.Contains(body, "/contrib_service/ttl_1d/fixture-upload") {
		t.Fatalf("the uploaded file never reached the request: %s", body[:min(len(body), 400)])
	}
	if !strings.Contains(body, "image/png") {
		t.Fatalf("the attachment lost its type: %s", body[:min(len(body), 400)])
	}
}

// The attachment slot is a list, and a turn that sends several files has to
// arrive with all of them, each keeping the type and name it was stored under.
func TestNativeTextCarriesEveryAttachment(t *testing.T) {
	server := nativeWebServer(t, "read")
	service, record := nativeService(t, server)

	result := invoke(t, service, "executor.execute", executorRequest{
		AuthID: record.ID, AuthProvider: provider, Model: flashModel,
		Format: "gemini", SourceFormat: "gemini", StorageJSON: jsonFixture(t, record),
		Payload: []byte(`{"contents":[{"role":"user","parts":[{"text":"look"},{"inlineData":{"mimeType":"image/png","data":"AAAA"}},{"inlineData":{"mimeType":"application/pdf","data":"AAAA"}}]}]}`),
	})

	if !result.OK {
		t.Fatalf("a turn carrying two attachments was refused: %+v", result.Error)
	}
	form, err := url.ParseQuery(nativeLastGeneration(t))
	if err != nil {
		t.Fatal(err)
	}
	body := form.Get("f.req")
	if count := strings.Count(body, "/contrib_service/ttl_1d/fixture-upload"); count != 2 {
		t.Fatalf("the request carried %d attachments, want 2: %s", count, body)
	}
	if !strings.Contains(body, "image/png") || !strings.Contains(body, "application/pdf") {
		t.Fatalf("an attachment lost its type: %s", body)
	}
	if !strings.Contains(body, "attachment-2.pdf") {
		t.Fatalf("the document was not stored under its own suffix: %s", body)
	}
}

// Each bridge spells inline media differently by the time it reaches the plugin.
// Reading one spelling meant an OpenAI image was refused as malformed and a
// Claude image was dropped without a word, which is the worse of the two.
func TestNativeTextAcceptsEveryBridgeSpellingOfInlineMedia(t *testing.T) {
	for name, parts := range map[string]string{
		"gemini": `{"inlineData":{"mimeType":"image/png","data":"AAAA"}}`,
		"openai": `{"inlineData":{"mime_type":"image/png","data":"AAAA"},"thoughtSignature":"sig"}`,
		"claude": `{"inline_data":{"mime_type":"image/png","data":"AAAA"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := nativeWebServer(t, "a colour")
			service, record := nativeService(t, server)

			result := invoke(t, service, "executor.execute", executorRequest{
				AuthID: record.ID, AuthProvider: provider, Model: flashModel,
				Format: "gemini", SourceFormat: "gemini", StorageJSON: jsonFixture(t, record),
				Payload: []byte(`{"contents":[{"role":"user","parts":[{"text":"look"},` + parts + `]}]}`),
			})

			if !result.OK {
				t.Fatalf("the %s spelling was refused: %+v", name, result.Error)
			}
			form, err := url.ParseQuery(nativeLastGeneration(t))
			if err != nil {
				t.Fatal(err)
			}
			if body := form.Get("f.req"); !strings.Contains(body, "/contrib_service/ttl_1d/fixture-upload") {
				t.Fatalf("the %s spelling never reached the request: %s", name, body)
			}
		})
	}
}

func TestNativeTextRejectsUndecodableMedia(t *testing.T) {
	server := nativeWebServer(t, "unused")
	service, record := nativeService(t, server)

	result := invoke(t, service, "executor.execute", executorRequest{
		AuthID: record.ID, AuthProvider: provider, Model: flashModel,
		Format: "gemini", SourceFormat: "gemini", StorageJSON: jsonFixture(t, record),
		Payload: []byte(`{"contents":[{"role":"user","parts":[{"text":"look"},{"inlineData":{"mimeType":"image/png","data":"!!!not base64!!!"}}]}]}`),
	})

	if result.OK || result.Error.Code != "gemini_web:attachment_encoding_invalid" && result.Error.Code != "attachment_encoding_invalid" {
		t.Fatalf("undecodable attachment was not refused: %+v", result.Error)
	}
}
