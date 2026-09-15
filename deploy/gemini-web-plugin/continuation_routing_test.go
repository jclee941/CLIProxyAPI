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
func TestContinuationSchedulerSelectsStoredAccount(t *testing.T) {
	// Given a receipt prepared on one of two accounts.
	service, local := continuationFixture(t)
	token := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`)).Token
	// When the interceptor passes routing data through supported header fields.
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
	picked := invoke(t, service, "scheduler.pick", map[string]any{"Provider": provider, "Model": flashModel, "Options": map[string]any{"Headers": response.Headers, "Metadata": map[string]string{"caller_scope": testCallerScope}}, "Candidates": []any{map[string]string{"ID": "other", "Provider": provider}, map[string]string{"ID": local.Target.ID, "Provider": provider}}})
	// Then the selected account is the receipt's owner, not the first candidate.
	if !picked.OK {
		t.Fatalf("pick: %+v", picked.Error)
	}
	var choice struct {
		Handled bool
		AuthID  string
	}
	if err := json.Unmarshal(picked.Result, &choice); err != nil {
		t.Fatal(err)
	}
	if !choice.Handled || choice.AuthID != local.Target.ID {
		t.Fatalf("choice: %+v", choice)
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
