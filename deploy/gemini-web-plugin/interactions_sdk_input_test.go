package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSDKUserInputStepUploadsAndExtendsVideo(t *testing.T) {
	// Given the exact input shape emitted by google-genai 2.24.0.
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	body := `{"input":[{"content":[{"data":"AAAA","mime_type":"video/mp4","type":"video"},{"text":"Continue the scene","type":"text"}],"type":"user_input"}],"model":"gemini-omni-1.1-flash","generation_config":{"video_config":{"task":"extend"}},"response_format":{"type":"video"}}`

	// When the SDK's request crosses the real plugin/HTTP boundary.
	interactionID(t, interactionCall(t, service, local, body))

	// Then the uploaded media remains the extension source.
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 || len(fixture.uploads) != 1 {
		t.Fatalf("submissions=%d uploads=%d", len(fixture.fields), len(fixture.uploads))
	}
	prompt, _ := jsonField(fixture.fields[0], 0, 0).(string)
	if !strings.Contains(prompt, "[# Sources <VIDEO_0>@Video1]") {
		t.Fatalf("SDK input lost its source role: %q", prompt)
	}
}

func TestInteractionRejectionUsesTheSDKStringErrorCode(t *testing.T) {
	response := interactionRejection(failure(400, "interaction_text_input_only"))
	var body struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(response.ResponseBody, &body); err != nil {
		t.Fatalf("official SDK cannot decode the error object: %v", err)
	}
	if response.StatusCode != 400 || body.Error.Code != "interaction_text_input_only" {
		t.Fatalf("error status/code changed: %+v %d", body.Error, response.StatusCode)
	}
}
