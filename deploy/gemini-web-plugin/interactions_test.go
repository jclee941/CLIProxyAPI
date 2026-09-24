package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The documented Omni input carries a reference image as inline bytes beside the
// text, and the web session uploads exactly that.
func TestInteractionCarriesAReferenceImage(t *testing.T) {
	_, payload, err := parseInteraction([]byte(`{"model":"gemini-omni-1.1-flash","input":[{"type":"text","text":"a cat"},{"type":"image","data":"AAAA","mime_type":"image/png"}]}`))

	if err != nil {
		t.Fatalf("a documented reference image was refused: %v", err)
	}
	if !strings.Contains(string(payload), `"inlineData"`) || !strings.Contains(string(payload), "image/png") {
		t.Fatalf("the reference never reached the payload: %s", payload)
	}
	if !strings.Contains(string(payload), "a cat") {
		t.Fatalf("the prompt was lost beside the reference: %s", payload)
	}
}

// A caller that is told only "failed" has nothing to act on, and the plugin
// already knows why.
func TestRenderedInteractionCarriesTheFailureReason(t *testing.T) {
	result, err := renderInteraction("account.json", continuationResult{},
		continuationView{Token: "t", State: "outcome_unknown", Error: "no_video_generated"})

	if err != nil {
		t.Fatal(err)
	}
	payload := string(result.(continuationResult).Payload)
	if !strings.Contains(payload, `"status":"failed"`) {
		t.Fatalf("a failed turn was not reported as failed: %s", payload)
	}
	if !strings.Contains(payload, "no_video_generated") {
		t.Fatalf("the reason was dropped on the way out: %s", payload)
	}
}

// An omni turn asked for a video. A turn that answered in prose did not produce
// one, whatever else it says, so it can never be rendered as a completed
// interaction: a caller that reads status alone would take the text for a clip.
func TestATextAnswerIsNeverACompletedInteraction(t *testing.T) {
	spoken, err := json.Marshal(map[string]any{"candidates": []any{map[string]any{
		"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": "I can't make that video."}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := renderInteraction("account.json", continuationResult{Payload: spoken},
		continuationView{Token: "t", State: "complete"})

	if err == nil {
		t.Fatalf("a text answer was rendered as a finished interaction: %s", result.(continuationResult).Payload)
	}
	if code := safeCredentialCode(err); code != "invalid_interaction_video" {
		t.Fatalf("code = %s, want invalid_interaction_video", code)
	}
}

// The omni parser refuses unknown fields, so every spelling a bridge may use has
// to be named or a reference image dies in validation before it is ever uploaded.
func TestOmniRequestAcceptsBothInlineSpellings(t *testing.T) {
	for name, part := range map[string]string{
		"gemini": `{"inlineData":{"mimeType":"image/png","data":"AAAA"}}`,
		"openai": `{"inlineData":{"mime_type":"image/png","data":"AAAA"},"thoughtSignature":"sig"}`,
		"claude": `{"inline_data":{"mime_type":"image/png","data":"AAAA"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := omniRequest([]byte(`{"contents":[{"role":"user","parts":[{"text":"a cat"},` + part + `]}]}`)); err != nil {
				t.Fatalf("the %s spelling was refused: %v", name, err)
			}
		})
	}
}

// The web session reads video as readily as it reads an image, and the upload
// path already carries it, so the input block the SDK defines for video is
// accepted on the same terms: inline bytes, or a Drive file to fetch.
func TestInteractionCarriesAVideoReference(t *testing.T) {
	for name, part := range map[string]string{
		"inline": `{"type":"video","data":"AAAA","mime_type":"video/mp4"}`,
		"drive":  `{"type":"video","uri":"https://drive.google.com/file/d/1A2B3C4D5E6F7G8H/view"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, payload, err := parseInteraction([]byte(`{"model":"gemini-omni-1.1-flash","input":[{"type":"text","text":"extend this"},` + part + `]}`))

			if err != nil {
				t.Fatalf("a %s video reference was refused: %v", name, err)
			}
			if !strings.Contains(string(payload), "extend this") {
				t.Fatalf("the prompt was lost beside the video: %s", payload)
			}
			carrier := `"inlineData"`
			if name == "drive" {
				carrier = `"fileData"`
			}
			if !strings.Contains(string(payload), carrier) {
				t.Fatalf("the %s video never reached the payload as %s: %s", name, carrier, payload)
			}
		})
	}
}

func TestInteractionPreservesFilesReferenceForCallerScopedResolution(t *testing.T) {
	_, payload, err := parseInteraction([]byte(`{"model":"gemini-omni-1.1-flash","input":[{"type":"text","text":"a cat"},{"type":"image","uri":"files/abc"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"fileUri":"files/abc"`) {
		t.Fatal("Files reference was dropped before caller-scoped resolution")
	}
}

func interactionCall(t *testing.T, service *service, local localSession, body string) envelope {
	t.Helper()
	auth, err := authFromRecord(local.Target)
	if err != nil {
		t.Fatal(err)
	}
	request := executorRequest{AuthID: local.Target.ID, AuthProvider: provider, Model: interactionOmniModel, Format: "interactions", SourceFormat: "interactions", StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, Payload: []byte(body), HostCallbackID: "fixture-generation"}
	request.Metadata.CallerScope = testCallerScope
	return invoke(t, service, "executor.execute", request)
}
func interactionID(t *testing.T, result envelope) string {
	t.Helper()
	if !result.OK {
		t.Fatalf("interaction failed: %+v", result.Error)
	}
	var response struct{ Payload []byte }
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	var body struct {
		ID     string `json:"id"`
		Object string `json:"object"`
		Status string `json:"status"`
		Steps  []struct {
			Type    string `json:"type"`
			Content []struct {
				Type     string `json:"type"`
				MIMEType string `json:"mime_type"`
				Data     string `json:"data"`
			} `json:"content"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(response.Payload, &body); err != nil {
		t.Fatal(err)
	}
	if body.ID == "" || body.Object != "interaction" || body.Status != "completed" || len(body.Steps) != 1 || body.Steps[0].Type != "model_output" || len(body.Steps[0].Content) != 1 || body.Steps[0].Content[0].Type != "video" || body.Steps[0].Content[0].MIMEType != "video/mp4" || body.Steps[0].Content[0].Data == "" {
		t.Fatalf("invalid official response: %s", response.Payload)
	}
	return body.ID
}
func TestInteractionsCreateChainsOfficialPreviousID(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true}
	continuationWeb(t, service, fixture)
	host := &loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}
	service.host = host.call
	first := interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first","response_format":{"type":"video","aspect_ratio":"16:9"}}`))
	// When a new official create call references the stored interaction.
	next := interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"edit","previous_interaction_id":"`+first+`"}`))
	// Then the new turn continues that conversation rather than uploading the
	// video back to the account that just made it.
	if next == first {
		t.Fatal("new turn reused interaction ID")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 2 || jsonField(fixture.fields[1][2], 0) != "c_chat" || jsonField(fixture.fields[1][2], 1) != "r_1" || jsonField(fixture.fields[1][2], 2) != "rc_1" {
		t.Fatalf("chaining: %#v", fixture.fields)
	}
}
func TestInteractionsStoreFalsePreventsFollowup(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	first := interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first","store":false}`))
	// When a caller tries to edit a non-stored interaction.
	result := interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"edit","previous_interaction_id":"`+first+`"}`)
	// Then it is rejected without another generation.
	if result.OK {
		t.Fatal("store=false interaction resumed")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 {
		t.Fatal("failed previous ID caused submission")
	}
}
func TestInteractionsRejectUnsupportedOptionsBeforeNetwork(t *testing.T) {
	service, local := continuationFixture(t)
	for _, body := range []string{
		`{"model":"gemini-omni-1.1-flash","input":"x","background":true,"store":false}`,
		`{"model":"gemini-omni-1.1-flash","input":"x","stream":"true"}`,
		`{"model":"gemini-omni-1.1-flash","input":"x","response_format":{"delivery":"unsupported"}}`,
		`{"model":"gemini-omni-1.1-flash","input":"x","response_format":{"resolution":"4k"}}`,
		`{"model":"gemini-omni-1.1-flash","input":[{"type":"video","uri":"https://example.invalid/video"}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			if result := interactionCall(t, service, local, body); result.OK {
				t.Fatal("unsupported interaction accepted")
			}
		})
	}
}
