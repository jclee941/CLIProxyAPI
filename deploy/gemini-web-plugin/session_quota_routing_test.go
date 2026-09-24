package main

import (
	"testing"
	"time"
)

func TestNewRequestsRoundRobinAfterFilteringExhaustedAccounts(t *testing.T) {
	service, local := continuationFixture(t)
	ids := []string{local.Target.ID}
	for _, seed := range []string{"b", "c", "d"} {
		record := recordFixture(t, seed)
		seedSession(t, service, record, sessionToken{encodedToken("seed-" + seed)})
		ids = append(ids, record.ID)
	}
	full, used := 1.0, 0.9
	reset := float64(service.now().Add(time.Hour).Unix())
	for _, id := range ids[:2] {
		service.observeQuota(id, &usageView{Metrics: []usageMetric{{WindowKind: "5h", UsageFraction: &full, ResetUnixSeconds: &reset}}})
	}
	service.observeQuota(ids[2], &usageView{Metrics: []usageMetric{{WindowKind: "5h", UsageFraction: &used}}})
	counts := map[string]int{}
	for range 8 {
		pick := service.pickServableAccount(interactionOmniModel, slotCandidates(ids...))
		counts[pick.AuthID]++
	}
	if counts[ids[2]] != 4 || counts[ids[3]] != 4 || len(counts) != 2 {
		t.Fatalf("uneven rotation among eligible accounts: %+v", counts)
	}
}

func TestWeeklyExhaustionExcludesAccountUntilItsReset(t *testing.T) {
	service, local := continuationFixture(t)
	other := recordFixture(t, "b")
	seedSession(t, service, other, sessionToken{encodedToken("other")})
	now := service.now()
	service.now = func() time.Time { return now }
	full := 1.0
	reset := float64(now.Add(7 * time.Hour).Unix())
	service.observeQuota(local.Target.ID, &usageView{Metrics: []usageMetric{{WindowKind: "weekly", UsageFraction: &full, ResetUnixSeconds: &reset}}})
	now = now.Add(20 * time.Minute)
	for range 4 {
		pick := service.pickServableAccount(interactionOmniModel, slotCandidates(local.Target.ID, other.ID))
		if pick.AuthID != other.ID {
			t.Fatalf("exhausted weekly quota was selected: %+v", pick)
		}
	}
	now = time.Unix(int64(reset), 0)
	pick := service.pickServableAccount(interactionOmniModel, slotCandidates(local.Target.ID))
	if !pick.Handled || pick.AuthID != local.Target.ID {
		t.Fatalf("account was not eligible after weekly reset: %+v", pick)
	}
}

func TestExhaustedFiveHourObservationDoesNotExpireBeforeReset(t *testing.T) {
	service, local := continuationFixture(t)
	now := service.now()
	service.now = func() time.Time { return now }
	full := 1.0
	reset := float64(now.Add(5 * time.Hour).Unix())
	service.observeQuota(local.Target.ID, &usageView{Metrics: []usageMetric{{WindowKind: "5h", UsageFraction: &full, ResetUnixSeconds: &reset}}})
	now = now.Add(20 * time.Minute)
	pick, err := service.pickContinuation(jsonFixture(t, map[string]any{
		"Provider": provider, "Model": interactionOmniModel,
		"Candidates": slotCandidates(local.Target.ID),
	}))
	if err == nil || pick.Handled || pick.AuthID != "" {
		t.Fatalf("exhausted fleet delegated back to host: pick=%+v err=%v", pick, err)
	}
}
