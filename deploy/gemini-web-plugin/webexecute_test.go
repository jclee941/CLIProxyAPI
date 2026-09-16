package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// nativeWebServer stands in for the web product: the bootstrap page, the
// capability RPC and the generation stream, each on the path prefix the
// credential's auth_user selects.
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

func TestNativeTextRejectsInlineMedia(t *testing.T) {
	server := nativeWebServer(t, "unused")
	service, record := nativeService(t, server)
	result := invoke(t, service, "executor.execute", executorRequest{
		AuthID: record.ID, AuthProvider: provider, Model: flashModel,
		Format: "gemini", SourceFormat: "gemini", StorageJSON: jsonFixture(t, record),
		Payload: []byte(`{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"AAAA"}}]}]}`),
	})
	if result.OK {
		t.Fatal("an image request was accepted by the text path")
	}
}
