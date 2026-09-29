package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestAFirstEntrySkipsAccountsPastTheirUsageCeilings(t *testing.T) {
	// Given one account at 80% of its five hours, one at 90% of its week and one
	// just below both, each with units enough for a room.
	service, local := continuationFixture(t)
	week := recordFixture(t, "b")
	seedSession(t, service, week, sessionToken{encodedToken("week")})
	below := recordFixture(t, "c")
	seedSession(t, service, below, sessionToken{encodedToken("below")})
	observe := func(id string, room, weekUsed float64) {
		plenty := 40000.0
		service.observeQuota(id, &usageView{Metrics: []usageMetric{
			{WindowKind: "5h", UsageFraction: &room, RemainingUnits: &plenty},
			{WindowKind: "weekly", UsageFraction: &weekUsed, RemainingUnits: &plenty},
		}})
	}
	observe(local.Target.ID, 0.80, 0.10)
	observe(week.ID, 0.10, 0.90)
	observe(below.ID, 0.79, 0.89)

	// When new requests are routed.
	chosen := map[string]int{}
	for range 6 {
		pick, err := service.pickContinuation(jsonFixture(t, map[string]any{
			"Provider": provider, "Model": interactionOmniModel,
			"Candidates": slotCandidates(local.Target.ID, week.ID, below.ID),
		}))
		if err != nil {
			t.Fatal(err)
		}
		chosen[pick.AuthID]++
	}

	// Then only the account below both ceilings takes them.
	if chosen[below.ID] != 6 {
		t.Fatalf("a first entry reached an account past its ceilings: %+v", chosen)
	}
}

func TestAContinuationStaysWithAnOwnerPastItsUsageCeiling(t *testing.T) {
	// Given a finished room whose owner now reads 85% of its five hours used.
	service, owner := continuationFixture(t)
	other := recordFixture(t, "b")
	seedSession(t, service, other, sessionToken{encodedToken("other")})
	continuationWeb(t, service, &continuationWebFixture{video: true})
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{owner.Target.ID: jsonFixture(t, owner.Target)}, service: service}).call
	first := interactionID(t, interactionCall(t, service, owner, `{"model":"gemini-omni-1.1-flash","input":"first"}`))
	used, left := 0.85, 7200.0
	service.observeQuota(owner.Target.ID, &usageView{Metrics: []usageMetric{{WindowKind: "5h", UsageFraction: &used, RemainingUnits: &left}}})

	// When its next turn is routed.
	pick, err := service.pickContinuation(jsonFixture(t, map[string]any{
		"Provider": provider, "Model": interactionOmniModel,
		"Options":    map[string]any{"Headers": http.Header{continuationHeader: {first}}, "Metadata": map[string]string{"caller_scope": testCallerScope}},
		"Candidates": slotCandidates(owner.Target.ID, other.ID),
	}))

	// Then it stays in the owner's conversation.
	if err != nil || pick.AuthID != owner.Target.ID {
		t.Fatalf("a continuation left an owner below exhaustion: pick=%+v err=%v", pick, err)
	}
}
