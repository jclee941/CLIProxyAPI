package main

import (
	"encoding/json"
	"testing"
)

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
	// Then a new video turn shares the exact upstream conversation and candidate.
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
		`{"model":"gemini-omni-1.1-flash","input":"x","background":true}`,
		`{"model":"gemini-omni-1.1-flash","input":"x","stream":"true"}`,
		`{"model":"gemini-omni-1.1-flash","input":"x","response_format":{"delivery":"uri"}}`,
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
