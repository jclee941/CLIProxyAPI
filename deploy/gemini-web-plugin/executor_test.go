package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestFlashExecutesSelectedSession_andBuffersNativeSSE(t *testing.T) {
	service := newService(nil)
	record := recordFixture(t, "a")
	service.secrets = &memorySecrets{tokens: map[string]sessionToken{record.TokenRef: {encodedToken("test-flash")}}}
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("x-goog-api-key") != encodedToken("test-flash") || request.Header.Get("Authorization") != "" {
			t.Error("incorrect outgoing credentials")
		}
		switch request.URL.Path {
		case "/v1/account-models":
			writeFixture(t, writer, `{"available":true,"models":[{"capability_id":"test-capability","display_name":"3.8 Flash","mode":1}]}`)
		case "/v1beta/models/gemini-3.8-flash:generateContent":
			var body struct {
				Model string            `json:"model"`
				Tools []json.RawMessage `json:"tools"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if request.Method != "POST" || body.Model != "gemini-3.8-flash" || len(body.Tools) != 1 {
				t.Error("native model/tool request changed")
			}
			writeFixture(t, writer, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"test_tool","args":{}}}]}}]}`)
		default:
			t.Errorf("unexpected path %s", request.URL.Path)
		}
	})
	result := invoke(t, service, "executor.execute_stream", executorRequest{AuthID: record.ID, AuthProvider: provider, Model: flashModel, Format: "gemini", SourceFormat: "gemini", Stream: true, StorageJSON: jsonFixture(t, record), Payload: []byte(`{"model":"gemini-web-flash-3.8","contents":[{"parts":[{"text":"test"}]}],"tools":[{"functionDeclarations":[{"name":"test_tool"}]}]}`)})
	if !result.OK {
		t.Fatalf("flash failed: %+v", result.Error)
	}
	var response struct {
		Chunks []struct{ Payload []byte } `json:"chunks"`
	}
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Chunks) != 1 || !strings.HasPrefix(string(response.Chunks[0].Payload), "data: ") || !strings.Contains(string(response.Chunks[0].Payload), "functionCall") {
		t.Fatal("missing native buffered SSE")
	}
}

func TestModelDiscoveryDoesNotAdvertiseOldFlashRegistry(t *testing.T) {
	service := newService(nil)
	record := recordFixture(t, "a")
	service.secrets = &memorySecrets{tokens: map[string]sessionToken{record.TokenRef: {encodedToken("test-registry")}}}
	localSidecar(t, service, func(writer http.ResponseWriter, _ *http.Request) {
		writeFixture(t, writer, `{"available":true,"models":[{"capability_id":"old","display_name":"3.7 Flash","mode":1}]}`)
	})
	result := invoke(t, service, "model.for_auth", executorRequest{AuthID: record.ID, AuthProvider: provider, StorageJSON: jsonFixture(t, record)})
	if !result.OK || strings.Contains(string(result.Result), flashModel) || strings.Contains(string(result.Result), "gemini-web-flash\"") {
		t.Fatal("unverified or legacy Flash registered")
	}
}

func TestOmniReturnsMP4WithoutImageConversion(t *testing.T) {
	service := newService(nil)
	record := recordFixture(t, "a")
	auth, err := authFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	service.secrets = &memorySecrets{tokens: map[string]sessionToken{record.TokenRef: {encodedToken("test-video")}}}
	video := base64.StdEncoding.EncodeToString([]byte("test-only-mp4-fixture"))
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/session/renew" {
			writeFixture(t, writer, `{"token":"`+encodedToken("test-video")+`"}`)
			return
		}
		if request.URL.Path != "/v1beta/models/gemini-web-omni:generateContent" {
			t.Error("video used wrong endpoint")
		}
		writeFixture(t, writer, `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"video/mp4","data":"`+video+`"}}]}}]}`)
	})
	payload := []byte(`{"contents":[{"role":"user","parts":[{"text":"test video"}]}]}`)
	result := invoke(t, service, "executor.execute", executorRequest{AuthID: record.ID, AuthProvider: provider, Model: omniModel, Format: "gemini", SourceFormat: "gemini", Payload: payload, OriginalRequest: payload, StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata})
	if !result.OK {
		t.Fatalf("video failed: %+v", result.Error)
	}
	var response struct{ Payload []byte }
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response.Payload), `"mimeType":"video/mp4"`) || !strings.Contains(string(response.Payload), video) || strings.Contains(string(response.Payload), "image/") {
		t.Fatal("video output was converted or lost")
	}
}

func TestOmniRejectsTranslatedChatOriginal_beforeResolving(t *testing.T) {
	service := newService(nil)
	auth, err := authFromRecord(recordFixture(t, "a"))
	if err != nil {
		t.Fatal(err)
	}
	result := invoke(t, service, "executor.execute", executorRequest{Model: omniModel, Format: "gemini", SourceFormat: "gemini", Payload: []byte(`{"contents":[{"parts":[{"text":"test"}]}]}`), OriginalRequest: []byte(`{"model":"gemini-web-omni","messages":[{"role":"user","content":"test"}]}`), AuthMetadata: auth.Metadata})
	if result.OK || result.Error.HTTPStatus != 400 {
		t.Fatal("translated chat request reached Omni")
	}
}
