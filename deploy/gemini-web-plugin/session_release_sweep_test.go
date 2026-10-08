package main

import (
	"testing"
	"time"
)

// A video turn whose stream drops leaves its account in submission_intent, and
// only a release returns it to new rooms. With the account listing as the only
// automatic release, two idle accounts sat out of every new room for six and
// eight hours on 2026-10-09 while Omni requests met account_unavailable.
func TestReleaseSweepReturnsAnAccountTrappedPastTheGenerationBudget(t *testing.T) {
	service, local := continuationFixture(t)
	continuationWeb(t, service, &continuationWebFixture{interrupted: true})
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first")))
	submitted := service.now()
	service.now = func() time.Time { return submitted.Add(webVideoBudget + time.Minute) }
	if pick := service.pickServableAccount(omniModel, slotCandidates(local.Target.ID)); pick.Handled {
		t.Fatalf("a trapped account was offered a new room: %+v", pick)
	}

	service.releaseTrappedSessions(t.Context())

	stored, err := service.sessions.read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != localReady || stored.ContinuationActive != "" || stored.AutoResolvedAt <= 0 {
		t.Fatalf("sweep left the account trapped: state=%s active=%q resolved=%d", stored.State, stored.ContinuationActive, stored.AutoResolvedAt)
	}
	if pick := service.pickServableAccount(omniModel, slotCandidates(local.Target.ID)); !pick.Handled || pick.AuthID != local.Target.ID {
		t.Fatalf("released account did not return to new rooms: %+v", pick)
	}
}

func TestReleaseSweepKeepsATurnRecoveryCanStillObserve(t *testing.T) {
	service, local := continuationFixture(t)
	continuationWeb(t, service, &continuationWebFixture{interrupted: true})
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first")))

	service.releaseTrappedSessions(t.Context())

	stored, err := service.sessions.read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != localSubmitting || stored.ContinuationActive == "" || stored.AutoResolvedAt != 0 {
		t.Fatalf("sweep released a turn recovery can observe: state=%s active=%q resolved=%d", stored.State, stored.ContinuationActive, stored.AutoResolvedAt)
	}
}
