package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestContinuationUnknownSubmissionNeverResubmits(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{missingHandles: true}
	continuationWeb(t, service, fixture)
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	body := submitContinuationBody(prepared.Token, "first")
	unknown := continuationReceipt(t, continuationCall(t, service, local, body))
	if unknown.State != "outcome_unknown" {
		t.Fatalf("initial state: %+v", unknown)
	}
	// When an ambiguous submission is retried with its existing receipt.
	replay := continuationReceipt(t, continuationCall(t, service, local, body))
	// Then uncertainty remains explicit and no second submission is made.
	if replay.State != "outcome_unknown" {
		t.Fatalf("replay: %+v", replay)
	}
	stored, err := service.sessions.read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != localSubmitting || stored.ContinuationActive != continuationKey(prepared.Token) {
		t.Fatal("unknown submission intent was cleared")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 {
		t.Fatal("unknown submission was repeated")
	}
}

func TestContinuationRecoveryRejectsWrongReply(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{interrupted: true, wrongReply: true}
	continuationWeb(t, service, fixture)
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first")))
	// When the read endpoint supplies another reply in the same conversation.
	result := continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"recover","token":"`+prepared.Token+`"}}`)
	// Then no unrelated candidate can be returned as the stored interaction.
	if result.OK || result.Error.Code != "gemini_web_omni:continuation_operation_mismatch" {
		t.Fatalf("wrong reply: %+v", result.Error)
	}
}

func TestContinuationHoldsLeaseDuringSubmission(t *testing.T) {
	service, local := continuationFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finish := sync.OnceFunc(func() { close(release) })
	defer finish()
	fixture := &continuationWebFixture{beforeSubmit: func() {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			t.Error("submission release timed out")
		}
	}}
	continuationWeb(t, service, fixture)
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	auth, err := authFromRecord(local.Target)
	if err != nil {
		t.Fatal(err)
	}
	execution := executorRequest{AuthID: local.Target.ID, AuthProvider: provider, Model: flashModel, Format: "gemini", SourceFormat: "gemini", AuthMetadata: auth.Metadata, StorageJSON: auth.StorageJSON, Payload: []byte(submitContinuationBody(prepared.Token, "first"))}
	execution.Metadata.CallerScope = testCallerScope
	request := jsonFixture(t, execution)
	completed := make(chan []byte, 1)
	go func() { completed <- service.handle(ctx, "executor.execute", request) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("submission did not start")
	}
	// When a second request tries to inspect the in-flight credential.
	lease, err := service.acquireCredential(local.Target.TokenRef, true)
	if err == nil {
		lease.guard.Unlock()
	}
	// Then the account's exclusive lease prevents concurrent submission/upkeep.
	if safeCredentialCode(err) != "session_busy" {
		t.Fatalf("lease: %v", err)
	}
	// Release the upstream and join the exact completion signal without a delay.
	finish()
	select {
	case <-completed:
	case <-time.After(5 * time.Second):
		t.Fatal("submission did not stop")
	}
}
