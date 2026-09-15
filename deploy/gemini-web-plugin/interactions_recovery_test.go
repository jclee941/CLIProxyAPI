package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestInteractionsPollRetainsRotatedProjectionWithoutResubmit(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true, pending: true, rotate: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	observed := 0
	service.continuationWait = func(context.Context) error {
		// The pending observation itself triggers readiness; no delay or timing luck.
		observed++
		fixture.mu.Lock()
		fixture.pending = false
		fixture.mu.Unlock()
		return nil
	}
	// When one synchronous official POST renews its credential then polls.
	interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`))
	// Then its follow-up read uses the new durable projection and never resubmits.
	if observed != 1 {
		t.Fatalf("pending observations: %d", observed)
	}
	stored, err := service.sessions.read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Target.SessionRevision != 2 {
		t.Fatalf("revision: %d", stored.Target.SessionRevision)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 {
		t.Fatal("poll submitted another turn")
	}
}

func TestInteractionsExpiredAuthenticationIsNotAnUnknownSubmission(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{expired: true}
	continuationWeb(t, service, fixture)
	// When the stored login is definitively expired before submission.
	result := interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`)
	// Then operator authentication is distinguished from an ambiguous paid action.
	if result.OK || result.Error.HTTPStatus != 401 || result.Error.Code != "gemini_web_omni:auth_error" {
		t.Fatalf("authentication failure: %+v", result.Error)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 0 {
		t.Fatal("expired account submitted")
	}
}

func TestContinuationMixedSchedulerBindsOwner(t *testing.T) {
	service, local := continuationFixture(t)
	token := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`)).Token
	// Real CPA multi-provider selection leaves Provider empty and sets Providers.
	result := invoke(t, service, "scheduler.pick", map[string]any{"Providers": []string{provider}, "Model": interactionOmniModel, "Options": map[string]any{"Headers": map[string][]string{continuationHeader: {token}}, "Metadata": map[string]string{"caller_scope": testCallerScope}}, "Candidates": []any{map[string]string{"ID": local.Target.ID, "Provider": provider}}})
	if !result.OK {
		t.Fatal(result.Error)
	}
	var picked continuationPick
	if err := json.Unmarshal(result.Result, &picked); err != nil {
		t.Fatal(err)
	}
	if !picked.Handled || picked.AuthID != local.Target.ID {
		t.Fatalf("pick: %+v", picked)
	}
}
