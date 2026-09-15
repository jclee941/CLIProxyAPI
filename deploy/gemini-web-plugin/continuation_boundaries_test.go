package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestContinuationPendingAccountRemainsRoutableAfterRestart(t *testing.T) {
	// Given a restarted account with a durable continuation submission intent.
	service, local := continuationFixture(t)
	continuationWeb(t, service, &continuationWebFixture{interrupted: true})
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first")))
	service.leases.get(local.Target.TokenRef).set(credentialState{state: maintenanceOperator})
	// When the host discovers models before routing a recovery request.
	result := invoke(t, service, "model.for_auth", struct {
		AuthID, AuthProvider string
		StorageJSON          []byte
	}{local.Target.ID, provider, jsonFixture(t, local.Target)})
	// Then the recovery model remains selectable, while stateless execution is fenced.
	if !result.OK {
		t.Fatalf("pending model discovery: %+v", result.Error)
	}
	var models struct{ Models []modelInfo }
	if err := json.Unmarshal(result.Result, &models); err != nil {
		t.Fatal(err)
	}
	if len(models.Models) == 0 {
		t.Fatal("no recovery model")
	}
	if _, err := service.resolveLocal(local.Target); safeCredentialCode(err) != "needs_operator" {
		t.Fatalf("stateless fence: %v", err)
	}
}

func TestContinuationDoesNotReleaseIntentFromAccountListing(t *testing.T) {
	service, local := continuationFixture(t)
	continuationWeb(t, service, &continuationWebFixture{interrupted: true})
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first")))
	// When credential-only recovery attempts to release a generation intent.
	_, _, err := service.releaseInterruptedSession(context.Background(), local.Target, true)
	// Then generation recovery, not a valid login, is required to release it.
	if safeCredentialCode(err) != "continuation_recovery_required" {
		t.Fatalf("release: %v", err)
	}
}

func TestOperatorReleasesIntentOfTurnRecoveryCanNeverObserve(t *testing.T) {
	service, local := continuationFixture(t)
	continuationWeb(t, service, &continuationWebFixture{missingHandles: true})
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	unknown := continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first")))
	if unknown.State != "outcome_unknown" {
		t.Fatalf("submission state: %+v", unknown)
	}
	// The automatic run still refuses to end an ambiguous submission by itself.
	if _, _, err := service.releaseInterruptedSession(context.Background(), local.Target, true); safeCredentialCode(err) != "continuation_recovery_required" {
		t.Fatalf("automatic release: %v", err)
	}
	// When the operator consents to end a turn that recorded no upstream operation.
	state, credential, err := service.releaseInterruptedSession(context.Background(), local.Target, false)
	if err != nil || state != maintenanceReady || credential != "valid" {
		t.Fatalf("operator release: state=%s credential=%s err=%v", state, credential, err)
	}
	stored, err := service.sessions.read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	// Then the account accepts new generations again.
	if stored.State != localReady || stored.ContinuationActive != "" {
		t.Fatalf("intent still pinned: state=%s active=%s", stored.State, stored.ContinuationActive)
	}
	turns, err := continuationTurns(stored)
	if err != nil {
		t.Fatal(err)
	}
	// And the ended turn is never mistaken for a completed one.
	if ended := turns[continuationKey(prepared.Token)]; ended.State != "no_operation" {
		t.Fatalf("turn not marked terminal: %+v", ended)
	}
}

func TestOperatorDefersToRecoveryWhileTheTurnRemainsObservable(t *testing.T) {
	service, local := continuationFixture(t)
	continuationWeb(t, service, &continuationWebFixture{interrupted: true})
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first")))
	// When an operator tries to end a submission upstream still holds an operation for.
	_, _, err := service.releaseInterruptedSession(context.Background(), local.Target, false)
	// Then generation recovery, not operator consent, must answer for it.
	if safeCredentialCode(err) != "continuation_recovery_required" {
		t.Fatalf("operator release: %v", err)
	}
}

func TestContinuationIntentPersistenceFailurePreventsSubmission(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{}
	continuationWeb(t, service, fixture)
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	service.sessions.fault = func(string) error { return errors.New("synthetic disk failure") }
	// When the durable intent cannot be committed.
	result := continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first"))
	// Then no upstream submission happens.
	if result.OK {
		t.Fatal("failed persistence accepted")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 0 {
		t.Fatal("submitted before durable intent")
	}
}

func TestContinuationReceiptRejectsDifferentAccount(t *testing.T) {
	service, owner := continuationFixture(t)
	receipt := continuationReceipt(t, continuationCall(t, service, owner, `{"geminiWebContinuation":{"action":"prepare"}}`))
	other := owner
	other.Target.ID = "gemini-web-other.json"
	other.Target.TokenRef = "session://gemini-web/" + strings.Repeat("b", 32)
	auth, err := authFromRecord(other.Target)
	if err != nil {
		t.Fatal(err)
	}
	other.Projection = string(auth.StorageJSON)
	if err := service.sessions.write(other); err != nil {
		t.Fatal(err)
	}
	// When a real receipt is presented with a different selected auth record.
	result := continuationCall(t, service, other, `{"geminiWebContinuation":{"action":"recover","token":"`+receipt.Token+`"}}`)
	// Then the binding is rejected before credential inspection or generation.
	if result.OK || result.Error.Code != "gemini_web_omni:continuation_identity_mismatch" {
		t.Fatalf("cross-account: %+v", result.Error)
	}
}
