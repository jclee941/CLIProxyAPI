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

	pick := service.pickServableAccount(slotCandidates(local.Target.ID, other.ID))

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

	pick := service.pickServableAccount(slotCandidates(local.Target.ID))

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
		pick := service.pickServableAccount(candidates)
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

	pick := service.pickServableAccount(slotCandidates(local.Target.ID, other.ID))

	if !pick.Handled || pick.AuthID != other.ID {
		t.Fatalf("scheduler queued behind an in-flight generation: %+v", pick)
	}
}
