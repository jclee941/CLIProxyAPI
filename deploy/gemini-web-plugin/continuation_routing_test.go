package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func continuationCall(t *testing.T, service *service, local localSession, body string) envelope {
	t.Helper()
	auth, err := authFromRecord(local.Target)
	if err != nil {
		t.Fatal(err)
	}
	request := executorRequest{AuthID: local.Target.ID, AuthProvider: provider, Model: flashModel, Format: "gemini", SourceFormat: "gemini", StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, Payload: []byte(body)}
	request.Metadata.CallerScope = testCallerScope
	return invoke(t, service, "executor.execute", request)
}
func continuationReceipt(t *testing.T, result envelope) continuationView {
	t.Helper()
	if !result.OK {
		t.Fatalf("execution: %+v", result.Error)
	}
	var response struct{ Payload []byte }
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	var body struct {
		View continuationView `json:"geminiWebContinuation"`
	}
	if err := json.Unmarshal(response.Payload, &body); err != nil {
		t.Fatal(err)
	}
	return body.View
}
func TestInteractionContinuationPrefersTheOwner(t *testing.T) {
	// Given a receipt prepared on one account.
	service, local := continuationFixture(t)
	token := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`)).Token
	// When a create names it, the interceptor passes the owner to the scheduler.
	intercepted := invoke(t, service, "request.intercept_before", struct {
		SourceFormat, Model string
		Body                []byte
		Metadata            map[string]string
	}{"interactions", interactionOmniModel, []byte(`{"model":"gemini-omni-1.1-flash","input":"next","previous_interaction_id":"` + token + `"}`), map[string]string{"caller_scope": testCallerScope}})
	if !intercepted.OK {
		t.Fatal(intercepted.Error)
	}
	var response struct{ Headers http.Header }
	if err := json.Unmarshal(intercepted.Result, &response); err != nil {
		t.Fatal(err)
	}
	if response.Headers.Get(continuationHeader) != token {
		t.Fatal("the create did not name the account holding the previous interaction")
	}
	// Then the owner is chosen when it can serve, which keeps the chain in one
	// conversation and off the uploaded-video rules.
	picked := invoke(t, service, "scheduler.pick", map[string]any{"Provider": provider, "Model": flashModel, "Options": map[string]any{"Headers": response.Headers, "Metadata": map[string]string{"caller_scope": testCallerScope}}, "Candidates": []any{map[string]string{"ID": "other", "Provider": provider}, map[string]string{"ID": local.Target.ID, "Provider": provider}}})
	if !picked.OK {
		t.Fatalf("pick: %+v", picked.Error)
	}
	var choice continuationPick
	if err := json.Unmarshal(picked.Result, &choice); err != nil {
		t.Fatal(err)
	}
	if !choice.Handled || choice.AuthID != local.Target.ID {
		t.Fatalf("choice: %+v", choice)
	}
	// An owner not offered cannot be replaced while it has no finished video to carry.
	delegated := invoke(t, service, "scheduler.pick", map[string]any{"Provider": provider, "Model": flashModel, "Options": map[string]any{"Headers": response.Headers, "Metadata": map[string]string{"caller_scope": testCallerScope}}, "Candidates": []any{map[string]string{"ID": "other", "Provider": provider}}})
	if delegated.OK || delegated.Error == nil || delegated.Error.HTTPStatus != 409 || !strings.Contains(delegated.Error.Code, "continuation_account_unavailable") {
		t.Fatalf("an unavailable owner did not fail closed: %+v", delegated)
	}
}
func TestContinuationRejectsUntrustedIdentifiersBeforeNetwork(t *testing.T) {
	service, local := continuationFixture(t)
	for _, body := range []string{
		`{"geminiWebContinuation":{"action":"recover","token":"` + strings.Repeat("0", 64) + `"}}`,
		`{"geminiWebContinuation":{"action":"prepare","conversation":"c_untrusted"}}`,
		`{"geminiWebContinuation":{"action":"recover","token":"tampered"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			// When an unknown receipt or raw upstream identifier crosses the boundary.
			result := continuationCall(t, service, local, body)
			// Then no account or upstream state is disclosed or submitted.
			if result.OK {
				t.Fatal("untrusted continuation accepted")
			}
		})
	}
}
