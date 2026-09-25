package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// translatedOmniRequest is the exact Gemini body the host produces from an
// OpenAI chat request for the Omni model, including the fixed safety thresholds
// its translation adds.
const translatedOmniRequest = `{"contents":[{"role":"user","parts":[{"text":"A yellow balloon floating up into a blue sky."}]}],"model":"gemini-web-omni","safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]}`

const openAIOmniOriginal = `{"model":"gemini-web-omni","messages":[{"role":"user","content":"A yellow balloon floating up into a blue sky."}]}`

func omniSidecarFixture(t *testing.T, service *service, submitted *[]byte) {
	t.Helper()
	video := base64.StdEncoding.EncodeToString([]byte("test-only-mp4-fixture"))
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if sidecarPath(request) == "/v1/session/renew" {
			writeFixture(t, writer, `{"token":"`+encodedToken("test-video")+`"}`)
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		*submitted = body
		writeFixture(t, writer, `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"video/mp4","data":"`+video+`"}}]}}]}`)
	})
}

func TestOmniAcceptsHostTranslatedChatRequest_andRebuildsTheUpstreamBody(t *testing.T) {
	service := newService(nil)
	record := recordFixture(t, "a")
	auth, err := authFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	seedSessions(t, service, map[string]sessionToken{record.TokenRef: {encodedToken("test-video")}})
	var submitted []byte
	omniSidecarFixture(t, service, &submitted)

	result := invoke(t, service, "executor.execute", executorRequest{
		AuthID:          record.ID,
		AuthProvider:    provider,
		Model:           omniModel,
		HostCallbackID:  "scope-list",
		Format:          "gemini",
		SourceFormat:    "gemini",
		Payload:         []byte(translatedOmniRequest),
		OriginalRequest: []byte(openAIOmniOriginal),
		StorageJSON:     auth.StorageJSON,
		AuthMetadata:    auth.Metadata,
	})

	if !result.OK {
		t.Fatalf("host-translated chat request rejected: %+v", result.Error)
	}
	var upstream struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"contents"`
		SafetySettings json.RawMessage `json:"safetySettings"`
	}
	if err := json.Unmarshal(submitted, &upstream); err != nil {
		t.Fatalf("submitted body is not JSON: %v", err)
	}
	if len(upstream.SafetySettings) != 0 {
		t.Fatalf("translation artifacts reached the generation call: %s", submitted)
	}
	if len(upstream.Contents) != 1 || len(upstream.Contents[0].Parts) != 1 ||
		upstream.Contents[0].Parts[0].Text != "A yellow balloon floating up into a blue sky." {
		t.Fatalf("upstream prompt was not preserved: %s", submitted)
	}
	var response struct{ Payload []byte }
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response.Payload), `"mimeType":"video/mp4"`) {
		t.Fatalf("video lost on the way back: %s", response.Payload)
	}
}

func TestOmniRejectsUnsupportedChatOriginals_beforeSubmitting(t *testing.T) {
	for _, scenario := range []struct {
		name, original string
	}{
		{"multi_turn", `{"messages":[{"role":"user","content":"a"},{"role":"assistant","content":"b"},{"role":"user","content":"c"}]}`},
		{"no_user_turn", `{"messages":[{"role":"system","content":"be nice"}]}`},
		{"empty_messages", `{"messages":[]}`},
		{"streaming", `{"messages":[{"role":"user","content":"a"}],"stream":true}`},
		{"image_part", `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/a.png"}}]}]}`},
		{"other_model", `{"model":"gpt-5","messages":[{"role":"user","content":"a"}]}`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service := newService(nil)
			record := recordFixture(t, "a")
			auth, err := authFromRecord(record)
			if err != nil {
				t.Fatal(err)
			}
			seedSessions(t, service, map[string]sessionToken{record.TokenRef: {encodedToken("test-video")}})
			var submitted []byte
			omniSidecarFixture(t, service, &submitted)

			result := invoke(t, service, "executor.execute", executorRequest{
				AuthID:          record.ID,
				AuthProvider:    provider,
				Model:           omniModel,
				HostCallbackID:  "scope-list",
				Format:          "gemini",
				SourceFormat:    "gemini",
				Payload:         []byte(translatedOmniRequest),
				OriginalRequest: []byte(scenario.original),
				StorageJSON:     auth.StorageJSON,
				AuthMetadata:    auth.Metadata,
			})

			if result.OK {
				t.Fatal("unsupported chat original reached Omni")
			}
			if len(submitted) != 0 {
				t.Fatalf("rejected request still generated a video: %s", submitted)
			}
			if result.Error == nil || result.Error.HTTPStatus != 400 {
				t.Fatalf("error=%+v", result.Error)
			}
		})
	}
}
