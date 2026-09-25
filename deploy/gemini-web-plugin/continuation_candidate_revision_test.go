package main

import "testing"

// Recovery re-reads the conversation after the candidate has been revised from
// placeholder to ready. generateVideo, the reader that demonstrably produces
// videos, validates the reply id and then takes the current candidate as-is;
// recovery must agree instead of demanding the stale candidate id.
func TestContinuationCandidateAcceptsRevisedCandidateUnderMatchingReply(t *testing.T) {
	ready := slots(2, map[int]any{0: "rc_ready", 1: "fresh"})
	entry := slots(4, map[int]any{
		0: []any{"c_chat", "r_turn"},
		3: []any{[]any{ready}},
	})
	body := []any{[]any{entry}}

	got, err := continuationCandidate(continuationTurn{Reply: "r_turn", Candidate: "rc_placeholder"}, body)
	if err != nil {
		t.Fatalf("revised candidate rejected under matching reply: %v", err)
	}
	if jsonField(got, 0) != "rc_ready" {
		t.Fatalf("candidate = %v, want the current rc_ready entry", jsonField(got, 0))
	}
}

// A different turn must still be refused: the reply id is the turn identity.
func TestContinuationCandidateStillRefusesAnotherReply(t *testing.T) {
	other := slots(2, map[int]any{0: "rc_other"})
	entry := slots(4, map[int]any{
		0: []any{"c_chat", "r_other"},
		3: []any{[]any{other}},
	})
	if _, err := continuationCandidate(continuationTurn{Reply: "r_turn", Candidate: "rc_placeholder"}, []any{[]any{entry}}); err == nil {
		t.Fatal("recovery bound to a different reply")
	}
}
