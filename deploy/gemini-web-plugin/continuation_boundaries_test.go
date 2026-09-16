package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
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

func TestNamedTurnWithoutCandidateDefersAccountRelease(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		name := "operator"
		if automatic {
			name = "automatic"
		}
		t.Run(name, func(t *testing.T) {
			service, local := continuationFixture(t)
			continuationWeb(t, service, &continuationWebFixture{})
			key := continuationKey(strings.Repeat("a", 64))
			local.State, local.ContinuationActive = localSubmitting, key
			if err := service.saveContinuations(local, map[string]continuationTurn{
				key: {CallerScope: testCallerScope, Model: omniModel, State: "submitting",
					Conversation: "c_chat", Reply: "r_turn", StartedAt: service.now().Unix()},
			}); err != nil {
				t.Fatal(err)
			}

			_, _, err := service.releaseInterruptedSession(t.Context(), local.Target, automatic)

			if safeCredentialCode(err) != "continuation_recovery_required" {
				t.Fatalf("named turn released without candidate: %v", err)
			}
			stored, err := service.sessions.read(local.Target.TokenRef)
			if err != nil {
				t.Fatal(err)
			}
			if stored.State != localSubmitting || stored.ContinuationActive != key {
				t.Fatal("recoverable turn lost its submission intent")
			}
		})
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

// A reply named without a conversation is the product answering the turn and
// declining to start a video on it. Reported as an outcome nobody observed, a
// caller has no reason to wait before asking again and every retry meets the
// same wall, so a video turn has to carry the refusal the product actually gave.
func TestAReplyWithoutAConversationReadsAsTheRefusalItIs(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		turn  continuationTurn
		state string
		code  string
	}{
		{"a video turn the product declined", continuationTurn{Model: omniModel, Reply: "r_turn"}, "no_video", "no_video_generated"},
		{"a video turn that named nothing at all", continuationTurn{Model: omniModel}, "no_operation", ""},
		{"a text turn keeps its own wording", continuationTurn{Model: flashModel, Reply: "r_turn"}, "no_operation", ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			state, refusal := unnamedOutcome(scenario.turn)

			if state != scenario.state {
				t.Fatalf("state = %q, want %q", state, scenario.state)
			}
			if scenario.code == "" {
				if refusal != nil {
					t.Fatalf("refusal = %v, want the turn left unobserved", refusal)
				}
				return
			}
			if safeCredentialCode(refusal) != scenario.code {
				t.Fatalf("refusal = %q, want %q", safeCredentialCode(refusal), scenario.code)
			}
		})
	}
}

// The account a declined turn ran on has to go back into rotation: nothing can
// be recovered without a conversation, so holding it until the generation budget
// expires only takes a working account out of service over an answer already in.
func TestADeclinedSubmissionReleasesItsAccount(t *testing.T) {
	service, local := continuationFixture(t)
	continuationWeb(t, service, &continuationWebFixture{replyOnly: true})
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))

	continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first"))

	stored, err := service.sessions.read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != localReady || stored.ContinuationActive != "" {
		t.Fatalf("account still pinned: state=%s active=%s", stored.State, stored.ContinuationActive)
	}
}

// A submit whose stream dies before it names an operation leaves nothing for
// recovery to find. Holding the account until the generation budget expires only
// takes a working account out of rotation for ten minutes over a turn already
// known to be unobservable, which is how a healthy fleet answers that it has no
// account to serve with.
func TestSubmitReleasesTheAccountWhenTheStreamDiesUnnamed(t *testing.T) {
	service, local := continuationFixture(t)
	continuationWeb(t, service, &continuationWebFixture{interrupted: true, missingHandles: true})
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))

	unknown := continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first")))

	if unknown.State != "outcome_unknown" {
		t.Fatalf("an unnamed dead stream was not reported as unknown: %+v", unknown)
	}
	stored, err := service.sessions.read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != localReady || stored.ContinuationActive != "" {
		t.Fatalf("account still pinned: state=%s active=%s", stored.State, stored.ContinuationActive)
	}
}

func TestAccountListingEndsATurnRecoveryCanNeverObserve(t *testing.T) {
	service, local := continuationFixture(t)
	continuationWeb(t, service, &continuationWebFixture{missingHandles: true})
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	unknown := continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first")))
	if unknown.State != "outcome_unknown" {
		t.Fatalf("submission state: %+v", unknown)
	}
	// When the run that lists accounts meets a turn that recorded no upstream
	// operation, which no recovery can ever find.
	state, credential, err := service.releaseInterruptedSession(context.Background(), local.Target, true)
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

// A turn past the generation budget has already answered its caller, so the run
// that lists accounts ends it instead of holding the account for a recovery that
// can only confirm the same thing. This is what a restart used to leave behind.
func TestAccountListingEndsATurnPastTheGenerationBudget(t *testing.T) {
	service, local := continuationFixture(t)
	continuationWeb(t, service, &continuationWebFixture{interrupted: true})
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first")))
	submitted := service.now()
	service.now = func() time.Time { return submitted.Add(webVideoBudget + time.Minute) }

	state, _, err := service.releaseInterruptedSession(context.Background(), local.Target, true)

	if err != nil || state != maintenanceReady {
		t.Fatalf("release past the budget: state=%s err=%v", state, err)
	}
	stored, err := service.sessions.read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != localReady || stored.ContinuationActive != "" {
		t.Fatalf("intent still pinned: state=%s active=%s", stored.State, stored.ContinuationActive)
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

func TestOperatorEndsAPinWhoseCredentialIsDefinitivelyRejected(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{interrupted: true}
	continuationWeb(t, service, fixture)
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first")))
	// The credential behind the pinned turn is now rejected, so recovery can
	// never read that turn again.
	fixture.mu.Lock()
	fixture.expired = true
	fixture.mu.Unlock()

	state, _, err := service.releaseInterruptedSession(context.Background(), local.Target, false)

	if err != nil {
		t.Fatalf("a dead credential stranded the account: %v", err)
	}
	if state != maintenanceCooldown {
		t.Fatalf("release did not fence the rejected credential: %s", state)
	}
	stored, err := service.sessions.read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != localReady || stored.ContinuationActive != "" {
		t.Fatalf("intent still pinned: state=%s active=%s", stored.State, stored.ContinuationActive)
	}
}
