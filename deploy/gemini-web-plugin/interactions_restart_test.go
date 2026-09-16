package main

import (
	"encoding/json"
	"testing"
)

func TestInteractionsFollowupSurvivesStoreReopen(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	first := interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`))
	path := service.sessions.directory.Name()
	if err := service.sessions.close(); err != nil {
		t.Fatal(err)
	}
	store, err := openSessionStore(path, sessionKeyFixture())
	if err != nil {
		t.Fatal(err)
	}
	service.sessions = store
	service.leases = credentialLeases{}
	t.Cleanup(func() {
		if err := store.close(); err != nil {
			t.Error(err)
		}
	})
	// When official previous_interaction_id is reused after durable-store recovery.
	interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"next","previous_interaction_id":"`+first+`"}`))
	// Then the original upstream conversation remains the parent.
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 2 || jsonField(fixture.fields[1][2], 0) != "c_chat" || jsonField(fixture.fields[1][2], 1) != "r_1" {
		t.Fatalf("restart metadata: %#v", fixture.fields)
	}
}

func TestContinuationInterruptedSubmissionReplayOnlyReads(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{interrupted: true}
	continuationWeb(t, service, fixture)
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	submitted := submitContinuationBody(prepared.Token, "first")
	pending := continuationReceipt(t, continuationCall(t, service, local, submitted))
	if pending.State != "pending" {
		t.Fatalf("initial state: %+v", pending)
	}
	// When the same stored submission receipt is replayed internally.
	recovered := continuationReceipt(t, continuationCall(t, service, local, submitted))
	// Then it retrieves the original result rather than creating another turn.
	if recovered.State != "complete" {
		t.Fatalf("recovery: %+v", recovered)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 {
		t.Fatal("receipt replay resubmitted")
	}
}

func TestInteractionsRejectCustomPublicContinuationSurface(t *testing.T) {
	service, _ := continuationFixture(t)
	result := invoke(t, service, "request.intercept_before", struct {
		SourceFormat, Model string
		Body                []byte
	}{"gemini", flashModel, []byte(`{"geminiWebContinuation":{"action":"prepare"}}`)})
	var response requestInterceptResponse
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	if !response.Terminate || response.StatusCode != 400 {
		t.Fatalf("custom public surface accepted: %+v", response)
	}
}
