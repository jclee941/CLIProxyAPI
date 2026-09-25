package main

import (
	"context"
	"testing"
	"time"
)

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

// Two candidates only show that the rotation moves at all. The fleet is six, and
// a cursor that drifts onto a subset is invisible at two and plain here: with
// every account idle and equally fresh, each one has to take an equal share.
func TestSchedulerWalksTheWholeFleetEvenly(t *testing.T) {
	service, local := continuationFixture(t)
	ids := []string{local.Target.ID}
	for _, seed := range []string{"b", "c", "d", "e", "f"} {
		record := recordFixture(t, seed)
		seedSession(t, service, record, sessionToken{encodedToken("seed-" + seed)})
		ids = append(ids, record.ID)
	}
	candidates := slotCandidates(ids...)
	chosen := map[string]int{}

	for round := range len(ids) * 3 {
		pick := service.pickServableAccount(flashModel, candidates)
		if !pick.Handled {
			t.Fatalf("round %d was not placed", round)
		}
		chosen[pick.AuthID]++
	}

	if len(chosen) != len(ids) {
		t.Fatalf("rotation reached %d of %d accounts: %+v", len(chosen), len(ids), chosen)
	}
	for id, count := range chosen {
		if count != 3 {
			t.Fatalf("%s took %d turns of %d, want an even 3: %+v", id, count, len(ids)*3, chosen)
		}
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

// A turn waits for a slot rather than being told the account cannot serve it.
func TestWaitForSlotTakesTheGuardOnceTheTurnAheadEnds(t *testing.T) {
	lease := &credentialLease{}
	lease.guard.Lock()
	taken := make(chan bool, 1)
	go func() { taken <- waitForSlot(context.Background(), lease) }()

	lease.guard.Unlock()

	if !<-taken {
		t.Fatal("a turn gave up on a slot that freed")
	}
	lease.guard.Unlock()
}

func TestWaitForSlotStopsWhenTheCallerIsGone(t *testing.T) {
	lease := &credentialLease{}
	lease.guard.Lock()
	defer lease.guard.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if waitForSlot(ctx, lease) {
		t.Fatal("a cancelled caller still waited for the slot")
	}
}

func TestSchedulerQueuesAGenerationBehindABusyAccount_andSharesItForText(t *testing.T) {
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

	// The only account being mid-generation is a queue, not an outage. Refusing
	// the turn here is what made a fleet of healthy accounts answer that there
	// was no account at all; it is placed, and waits for the slot instead.
	if !generation.Handled || generation.AuthID != local.Target.ID {
		t.Fatalf("generation was refused while the fleet was merely busy: %+v", generation)
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

func TestAccountListingUsesOfficialOmniModelForGeneration(t *testing.T) {
	service, local := continuationFixture(t)
	service.startedAt = 0
	continuationWeb(t, service, &continuationWebFixture{interrupted: true})
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first")))

	stored, err := service.localStore().read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	turns, err := continuationTurns(stored)
	if err != nil {
		t.Fatal(err)
	}
	turn := turns[stored.ContinuationActive]
	turn.Model = omniModel
	turns[stored.ContinuationActive] = turn
	if err := service.saveContinuations(stored, turns); err != nil {
		t.Fatal(err)
	}

	activity := service.runningTurn(local.Target)

	if activity == nil || activity.Model != interactionOmniModel {
		t.Fatalf("the account listing exposed an internal model name: %+v", activity)
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

func quotaUsage(fraction float64, resetAt float64) *usageView {
	return &usageView{Metrics: []usageMetric{{UsageFraction: &fraction, ResetUnixSeconds: &resetAt, WindowKind: "5h", Unit: "provider_compute_unit"}}}
}

func TestSchedulerSkipsAnExhaustedAccount_untilItsWindowResets(t *testing.T) {
	service, local := continuationFixture(t)
	other := recordFixture(t, "b")
	seedSession(t, service, other, sessionToken{encodedToken("second")})
	service.observeQuota(local.Target.ID, quotaUsage(1, float64(service.now().Add(time.Hour).Unix())))
	service.observeQuota(other.ID, quotaUsage(0.10, 0))

	pick := service.pickServableAccount(flashModel, slotCandidates(local.Target.ID, other.ID))

	if !pick.Handled || pick.AuthID != other.ID {
		t.Fatalf("a spent account still took the turn: %+v", pick)
	}
}

func TestSchedulerSpreadsBetweenComparablyFreshAccounts(t *testing.T) {
	service, local := continuationFixture(t)
	other := recordFixture(t, "b")
	seedSession(t, service, other, sessionToken{encodedToken("second")})
	service.observeQuota(local.Target.ID, quotaUsage(0.10, 0))
	service.observeQuota(other.ID, quotaUsage(0.02, 0))
	candidates := slotCandidates(local.Target.ID, other.ID)
	chosen := map[string]int{}

	for round := 0; round < 4; round++ {
		chosen[service.pickServableAccount(flashModel, candidates).AuthID]++
	}

	if len(chosen) != 2 {
		t.Fatalf("a small quota gap concentrated the fleet: %+v", chosen)
	}
}

func TestSchedulerRoundRobinsDespiteDifferentHeadroom(t *testing.T) {
	service, local := continuationFixture(t)
	other := recordFixture(t, "b")
	seedSession(t, service, other, sessionToken{encodedToken("second")})
	service.observeQuota(local.Target.ID, quotaUsage(0.80, 0))
	service.observeQuota(other.ID, quotaUsage(0.05, 0))

	chosen := map[string]int{}
	for range 4 {
		chosen[service.pickServableAccount(flashModel, slotCandidates(local.Target.ID, other.ID)).AuthID]++
	}
	if chosen[local.Target.ID] != 2 || chosen[other.ID] != 2 {
		t.Fatalf("quota differences skewed round-robin: %+v", chosen)
	}
}

func TestSchedulerIncludesUnmeasuredAccountsWithoutFavoringThem(t *testing.T) {
	service, local := continuationFixture(t)
	other := recordFixture(t, "b")
	seedSession(t, service, other, sessionToken{encodedToken("second")})
	service.observeQuota(other.ID, quotaUsage(0.90, 0))

	chosen := map[string]int{}
	for range 4 {
		chosen[service.pickServableAccount(flashModel, slotCandidates(local.Target.ID, other.ID)).AuthID]++
	}
	if chosen[local.Target.ID] != 2 || chosen[other.ID] != 2 {
		t.Fatalf("unmeasured quota skewed round-robin: %+v", chosen)
	}
}

func TestSchedulerReleasesAnExhaustedAccountAfterTheReset(t *testing.T) {
	service, local := continuationFixture(t)
	service.observeQuota(local.Target.ID, quotaUsage(1, float64(service.now().Add(-time.Minute).Unix())))

	pick := service.pickServableAccount(flashModel, slotCandidates(local.Target.ID))

	if !pick.Handled || pick.AuthID != local.Target.ID {
		t.Fatalf("a reset window stayed closed: %+v", pick)
	}
}

func roomUsage(remaining float64) *usageView {
	return &usageView{Metrics: []usageMetric{{RemainingUnits: &remaining, WindowKind: "5h", Unit: "provider_compute_unit"}}}
}

func TestSchedulerOpensAVideoRoomOnlyWhereTheWholeRoomFits(t *testing.T) {
	service, local := continuationFixture(t)
	other := recordFixture(t, "b")
	seedSession(t, service, other, sessionToken{encodedToken("second")})
	service.observeQuota(local.Target.ID, roomUsage(1826))
	service.observeQuota(other.ID, roomUsage(45655))

	for range 4 {
		if pick := service.pickServableAccount(omniModel, slotCandidates(local.Target.ID, other.ID)); pick.AuthID != other.ID {
			t.Fatalf("a room opened where it cannot finish: %+v", pick)
		}
	}
}

// A room opened on an account that cannot pay for three videos fails on its
// second or third turn, where no other account can take it over.
func TestSchedulerOpensNoRoom_whenNoAccountAffordsOne(t *testing.T) {
	service, local := continuationFixture(t)
	other := recordFixture(t, "b")
	seedSession(t, service, other, sessionToken{encodedToken("second")})
	service.observeQuota(local.Target.ID, roomUsage(1826))
	service.observeQuota(other.ID, roomUsage(10561))

	if pick := service.pickServableAccount(omniModel, slotCandidates(local.Target.ID, other.ID)); pick.Handled {
		t.Fatalf("a room opened where three videos do not fit: %+v", pick)
	}
	_, err := service.pickContinuation(jsonFixture(t, map[string]any{
		"Provider": provider, "Model": interactionOmniModel,
		"Candidates": slotCandidates(local.Target.ID, other.ID),
	}))
	if safeCredentialCode(err) != "account_unavailable" {
		t.Fatalf("scheduler error = %v, want account_unavailable", err)
	}
}

func TestSchedulerLeavesTextTurnsOnTheRotation(t *testing.T) {
	service, local := continuationFixture(t)
	other := recordFixture(t, "b")
	seedSession(t, service, other, sessionToken{encodedToken("second")})
	service.observeQuota(local.Target.ID, roomUsage(1826))
	service.observeQuota(other.ID, roomUsage(45655))

	chosen := map[string]int{}
	for range 4 {
		chosen[service.pickServableAccount(flashModel, slotCandidates(local.Target.ID, other.ID)).AuthID]++
	}
	if chosen[local.Target.ID] != 2 || chosen[other.ID] != 2 {
		t.Fatalf("a text turn is not a room: %+v", chosen)
	}
}
