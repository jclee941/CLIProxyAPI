package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// translatedOmniRequest is the exact Gemini body the host produces from an
// OpenAI chat request for the Omni model, including the fixed safety thresholds
// its translation adds.
const translatedOmniRequest = `{"contents":[{"role":"user","parts":[{"text":"A yellow balloon floating up into a blue sky."}]}],"model":"gemini-web-omni","safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]}`

const openAIOmniOriginal = `{"model":"gemini-web-omni","messages":[{"role":"user","content":"A yellow balloon floating up into a blue sky."}]}`

// omniWebFixture serves a native video turn for the seeded session; the
// fixture records every generation the plugin submits.
func omniWebFixture(t *testing.T, service *service) *continuationWebFixture {
	t.Helper()
	fixture := &continuationWebFixture{video: true}
	continuationWeb(t, service, fixture)
	return fixture
}

func TestOmniAcceptsHostTranslatedChatRequest_andRebuildsTheUpstreamBody(t *testing.T) {
	service := newService(nil)
	record := recordFixture(t, "a")
	auth, err := authFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	seedSessions(t, service, map[string]sessionToken{record.TokenRef: {encodedToken("test-video")}})
	fixture := omniWebFixture(t, service)

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
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 {
		t.Fatalf("submissions: %d, want 1", len(fixture.fields))
	}
	submitted := string(jsonFixture(t, fixture.fields[0]))
	if strings.Contains(submitted, "HARM_CATEGORY") || strings.Contains(submitted, "safetySettings") {
		t.Fatalf("translation artifacts reached the generation call: %s", submitted)
	}
	if !strings.Contains(submitted, "A yellow balloon floating up into a blue sky.") {
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
			fixture := omniWebFixture(t, service)

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
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if len(fixture.fields) != 0 {
				t.Fatalf("rejected request still generated a video: %d submission(s)", len(fixture.fields))
			}
			if result.Error == nil || result.Error.HTTPStatus != 400 {
				t.Fatalf("error=%+v", result.Error)
			}
		})
	}
}
