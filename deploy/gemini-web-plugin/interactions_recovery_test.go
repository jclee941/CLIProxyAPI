package main

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
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

// A conversation and reply identify the turn even before a candidate arrives.
func TestATurnNamedWithoutItsVideoIsRecoveredNotDiscarded(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true, lateCandidate: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call

	interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`))

	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 {
		t.Fatalf("submissions: %d, want the turn recovered rather than repeated", len(fixture.fields))
	}
}

func TestInterruptedNamedTurnWithoutCandidateRecoversThroughGET(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true, lateCandidate: true, interrupted: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	result := interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`)
	if !result.OK {
		t.Fatal(result.Error)
	}
	var response struct{ Payload []byte }
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	var pending struct{ ID, Status string }
	if err := json.Unmarshal(response.Payload, &pending); err != nil {
		t.Fatal(err)
	}
	if pending.Status != "in_progress" {
		t.Fatalf("named interrupted turn: %s", response.Payload)
	}
	stored, err := service.sessions.read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != localSubmitting || stored.ContinuationActive != continuationKey(pending.ID) {
		t.Fatalf("recoverable turn lost its intent: state=%s active=%s", stored.State, stored.ContinuationActive)
	}
	turns, err := continuationTurns(stored)
	if err != nil {
		t.Fatal(err)
	}
	turn := turns[continuationKey(pending.ID)]
	if turn.Conversation == "" || turn.Reply == "" || turn.Candidate != "" {
		t.Fatalf("unexpected stored receipt: %+v", turn)
	}
	get := interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","id":"`+pending.ID+`"}`)
	get.Alt = interactionRetrieveAlt
	recovered := interactionID(t, invoke(t, service, "executor.execute", get))
	if recovered != pending.ID {
		t.Fatalf("recovery replaced receipt: %s != %s", recovered, pending.ID)
	}
	stored, err = service.sessions.read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != localReady || stored.ContinuationActive != "" {
		t.Fatal("completed recovery kept the account pinned")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 {
		t.Fatalf("GET resubmitted: %d submissions", len(fixture.fields))
	}
}

// The submit and the wait that follows it share one budget, so a submit that
// spends the budget leaves nothing for the recovery it just made possible: the
// loop breaks on its first deadline check and answers in_progress for a video
// the upstream may be moments from finishing. generateVideo starts its budget
// once the submit is done, and this path has to measure the same way.
func TestSubmitSpendingTheBudgetStillLeavesRecoveryItsOwn(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true, lateCandidate: true, pending: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	var submitted atomic.Bool
	started := service.now()
	service.now = func() time.Time {
		if submitted.Load() {
			return started.Add(webVideoBudget + time.Minute)
		}
		return started
	}
	fixture.beforeSubmit = func() { submitted.Store(true) }
	observed := 0
	service.continuationWait = func(context.Context) error {
		// The observation itself makes the video ready; no delay and no timing luck.
		observed++
		fixture.mu.Lock()
		fixture.pending = false
		fixture.mu.Unlock()
		return nil
	}

	interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`))

	if observed != 1 {
		t.Fatalf("recovery observations: %d, want the wait budget measured from the submit", observed)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 {
		t.Fatalf("submissions: %d, want the turn recovered rather than repeated", len(fixture.fields))
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
