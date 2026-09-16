package main

import "testing"

func slotCandidates(ids ...string) []struct{ ID, Provider string } {
	candidates := make([]struct{ ID, Provider string }, 0, len(ids))
	for _, id := range ids {
		candidates = append(candidates, struct{ ID, Provider string }{id, provider})
	}
	return candidates
}

func TestSchedulerSkipsAPinnedAccount_whenAnotherSessionCanServe(t *testing.T) {
	service, local := continuationFixture(t)
	other := recordFixture(t, "b")
	seedSession(t, service, other, sessionToken{encodedToken("second")})
	pinned := local
	pinned.State = localSubmitting
	pinned.ContinuationActive = "pinned"
	if err := service.sessions.write(pinned); err != nil {
		t.Fatal(err)
	}

	pick := service.pickServableAccount(flashModel, slotCandidates(local.Target.ID, other.ID))

	if !pick.Handled || pick.AuthID != other.ID {
		t.Fatalf("scheduler chose an unusable account: %+v", pick)
	}
}

func TestSchedulerDelegates_whenNoSessionCanServe(t *testing.T) {
	service, local := continuationFixture(t)
	pinned := local
	pinned.State = localRenewing
	if err := service.sessions.write(pinned); err != nil {
		t.Fatal(err)
	}

	pick := service.pickServableAccount(flashModel, slotCandidates(local.Target.ID))

	if pick.Handled || pick.AuthID != "" {
		t.Fatalf("scheduler claimed a request it cannot place: %+v", pick)
	}
}

func TestSchedulerSpreadsAcrossIdleAccounts(t *testing.T) {
	service, local := continuationFixture(t)
	other := recordFixture(t, "b")
	seedSession(t, service, other, sessionToken{encodedToken("second")})
	candidates := slotCandidates(local.Target.ID, other.ID)
	chosen := map[string]int{}

	for round := 0; round < 4; round++ {
		pick := service.pickServableAccount(flashModel, candidates)
		if !pick.Handled {
			t.Fatalf("round %d was not placed", round)
		}
		chosen[pick.AuthID]++
	}

	if len(chosen) != 2 {
		t.Fatalf("scheduler pinned every turn to one account: %+v", chosen)
	}
}

func TestSchedulerPrefersAnIdleAccountOverABusyOne(t *testing.T) {
	service, local := continuationFixture(t)
	other := recordFixture(t, "b")
	seedSession(t, service, other, sessionToken{encodedToken("second")})
	busy, err := service.acquireCredential(local.Target.TokenRef, true)
	if err != nil {
		t.Fatal(err)
	}
	defer busy.guard.Unlock()
	if _, err := service.accountLease(local.Target); err != nil {
		t.Fatal(err)
	}

	pick := service.pickServableAccount(flashModel, slotCandidates(local.Target.ID, other.ID))

	if !pick.Handled || pick.AuthID != other.ID {
		t.Fatalf("scheduler queued behind an in-flight generation: %+v", pick)
	}
}

func TestSchedulerRefusesABusyAccountForAGeneration_butSharesItForText(t *testing.T) {
	service, local := continuationFixture(t)
	busy, err := service.acquireCredential(local.Target.TokenRef, true)
	if err != nil {
		t.Fatal(err)
	}
	defer busy.guard.Unlock()
	if _, err := service.accountLease(local.Target); err != nil {
		t.Fatal(err)
	}
	candidates := slotCandidates(local.Target.ID)

	generation := service.pickServableAccount(omniModel, candidates)
	text := service.pickServableAccount(flashModel, candidates)

	if generation.Handled {
		t.Fatalf("generation queued behind an in-flight one: %+v", generation)
	}
	if !text.Handled || text.AuthID != local.Target.ID {
		t.Fatalf("text turn lost a shareable session: %+v", text)
	}
}

func TestAccountListingReportsAGenerationInsteadOfAnOperatorError(t *testing.T) {
	service, local := continuationFixture(t)
	service.startedAt = 0
	continuationWeb(t, service, &continuationWebFixture{interrupted: true})
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first")))

	activity := service.runningTurn(local.Target)

	if activity == nil || activity.Model == "" || activity.StartedAt == 0 {
		t.Fatalf("a submitted turn was not reported as activity: %+v", activity)
	}
}

func TestAccountListingReportsNoActivity_whenTheSessionIsIdle(t *testing.T) {
	service, local := continuationFixture(t)

	if activity := service.runningTurn(local.Target); activity != nil {
		t.Fatalf("idle session reported a generation: %+v", activity)
	}
}

func TestBusySessionStillReportsModelsAndUsage_whileGenerating(t *testing.T) {
	service, local := continuationFixture(t)
	continuationWeb(t, service, &continuationWebFixture{interrupted: true})
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first")))

	token, err := service.busySessionToken(local.Target)

	if err != nil || token.value == "" {
		t.Fatalf("a generating session hid its credential from inspection: %v", err)
	}
}

func TestBusySessionTokenRefusesAnIdleSession(t *testing.T) {
	service, local := continuationFixture(t)

	if _, err := service.busySessionToken(local.Target); safeCredentialCode(err) != "needs_operator" {
		t.Fatalf("idle session exposed a credential through the busy path: %v", err)
	}
}

func TestActivityIsNotClaimedForATurnFromABeforeRestart(t *testing.T) {
	service, local := continuationFixture(t)
	continuationWeb(t, service, &continuationWebFixture{interrupted: true})
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first")))
	// The process restarted after the turn was submitted.
	service.startedAt = service.now().Unix() + 1

	if activity := service.runningTurn(local.Target); activity != nil {
		t.Fatalf("an interrupted turn was reported as progress: %+v", activity)
	}
}
