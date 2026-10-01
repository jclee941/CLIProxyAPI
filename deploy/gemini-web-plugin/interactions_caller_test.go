package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInteractionsRejectOtherAuthenticatedCaller(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	first := interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`))
	auth, err := authFromRecord(local.Target)
	if err != nil {
		t.Fatal(err)
	}
	request := executorRequest{AuthID: local.Target.ID, AuthProvider: provider, Model: interactionOmniModel, Format: "interactions", SourceFormat: "interactions", StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, HostCallbackID: "fixture-generation", Payload: []byte(`{"model":"gemini-omni-1.1-flash","input":"forbidden edit","previous_interaction_id":"` + first + `"}`)}
	request.Metadata.CallerScope = strings.Repeat("d", 64)
	// When a second authenticated CPA principal presents a valid first-caller ID.
	result := invoke(t, service, "executor.execute", request)
	// Then account equality alone is insufficient and no generation is submitted.
	if result.OK || result.Error.Code != "gemini_web_omni:continuation_identity_mismatch" {
		t.Fatalf("cross-caller result: %+v", result.Error)
	}
	// The host answers a JSON message as it stands, so the caller reads the
	// identifier as code while the prefix the stop rules match stays in the text.
	// It also names the account the turn was routed to, by its short id.
	var body struct {
		Error map[string]string `json:"error"`
	}
	if err := json.Unmarshal([]byte(result.Error.Message), &body); err != nil {
		t.Fatalf("cross-caller error body: %s", result.Error.Message)
	}
	want := map[string]string{"account": interactionAccount(local.Target.ID), "code": "continuation_identity_mismatch", "message": "gemini_web_omni:continuation_identity_mismatch"}
	if len(body.Error) != len(want) || body.Error["account"] != want["account"] || body.Error["code"] != want["code"] || body.Error["message"] != want["message"] {
		t.Fatalf("cross-caller error body: %s", result.Error.Message)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 {
		t.Fatal("cross-caller request submitted")
	}
}

func TestContinuationSchedulerRejectsOtherAuthenticatedCaller(t *testing.T) {
	service, local := continuationFixture(t)
	token := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`)).Token
	// Caller identity is accepted only from host metadata, never a routing header.
	result := invoke(t, service, "scheduler.pick", map[string]any{"Provider": provider, "Options": map[string]any{"Metadata": map[string]string{"caller_scope": strings.Repeat("d", 64)}, "Headers": map[string][]string{continuationHeader: {token}, "Caller-Scope": {testCallerScope}}}, "Candidates": []any{map[string]string{"ID": local.Target.ID, "Provider": provider}}})
	if result.OK || result.Error.Code != "continuation_identity_mismatch" {
		t.Fatalf("cross-caller pick: %+v", result.Error)
	}
}

func TestInteractionSchedulerFailuresNameTheirCode(t *testing.T) {
	service, _ := continuationFixture(t)
	unknown := strings.Repeat("b", 64)
	for _, scenario := range []struct {
		name    string
		headers map[string][]string
		status  int
		code    string
	}{
		{"unknown-retrieval", map[string][]string{continuationHeader: {unknown}, interactionRetrieveHeader: {"true"}}, 404, "interaction_not_found"},
		{"unknown-continuation", map[string][]string{continuationHeader: {unknown}}, 400, "continuation_identity_mismatch"},
		{"no-room", map[string][]string{}, 409, "account_unavailable"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given an Interactions pick the scheduler has to refuse.
			result := invoke(t, service, "scheduler.pick", map[string]any{"Provider": provider, "Model": interactionOmniModel, "Options": map[string]any{"Metadata": map[string]string{"caller_scope": testCallerScope}, "Headers": scenario.headers}, "Candidates": []any{}})
			// Then the refusal keeps its status and names itself as the code.
			want := `{"error":{"code":"` + scenario.code + `","message":"` + scenario.code + `"}}`
			if result.OK || result.Error.HTTPStatus != scenario.status || result.Error.Code != scenario.code || result.Error.Message != want {
				t.Fatalf("pick refusal: %+v", result.Error)
			}
		})
	}
}

func TestContinuationSchedulerRejectsEmptyScopeEvenForUnscopedReceipt(t *testing.T) {
	service, local := continuationFixture(t)
	token := strings.Repeat("a", 64)
	if err := service.saveContinuations(local, map[string]continuationTurn{continuationKey(token): {Model: omniModel, State: "complete"}}); err != nil {
		t.Fatal(err)
	}
	// Empty caller metadata must not authorize an old receipt with empty ownership.
	result := invoke(t, service, "scheduler.pick", map[string]any{"Provider": provider, "Options": map[string]any{"Headers": map[string][]string{continuationHeader: {token}}}, "Candidates": []any{map[string]string{"ID": local.Target.ID, "Provider": provider}}})
	if result.OK || result.Error.Code != "interaction_requires_authenticated_caller_scope" {
		t.Fatalf("empty-scope pick: %+v", result.Error)
	}
}

func TestInteractionsRequireAuthenticatedCallerMetadata(t *testing.T) {
	service, local := continuationFixture(t)
	auth, err := authFromRecord(local.Target)
	if err != nil {
		t.Fatal(err)
	}
	result := invoke(t, service, "executor.execute", executorRequest{AuthID: local.Target.ID, AuthProvider: provider, Model: interactionOmniModel, Format: "interactions", SourceFormat: "interactions", StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, Payload: []byte(`{"model":"gemini-omni-1.1-flash","input":"first"}`)})
	if result.OK || result.Error.Code != "gemini_web_omni:interaction_requires_authenticated_caller_scope" {
		t.Fatalf("unscoped caller: %+v", result.Error)
	}
}
