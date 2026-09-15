package main

import (
	"strings"
	"testing"
)

// A single submission stream legitimately revises its operation identifiers as
// the video candidate moves from placeholder to ready. The proven non-continuation
// reader adopts the newest values; the observer must agree instead of failing.
func TestContinuationObserverAdoptsRevisedOperationIdsWithinOneSubmission(t *testing.T) {
	first := slots(26, map[int]any{1: []any{"c_chat", "r_turn"}, 4: []any{[]any{"rc_placeholder"}}})
	second := slots(26, map[int]any{1: []any{"c_chat", "r_turn"}, 4: []any{[]any{"rc_ready"}}})
	raw := string(jsonFixture(t, []any{[]any{"wrb.fr", nil, string(jsonFixture(t, first))}})) + "\n" +
		string(jsonFixture(t, []any{[]any{"wrb.fr", nil, string(jsonFixture(t, second))}})) + "\n"

	turn := continuationTurn{}
	_, err := readContinuationStream(strings.NewReader(raw), func(line []byte) error {
		updated, frameErr := continuationFrame(turn, line)
		if frameErr == nil {
			turn = updated
		}
		return frameErr
	})

	if err != nil {
		t.Fatalf("revised candidate rejected: %v", err)
	}
	if turn.Candidate != "rc_ready" {
		t.Fatalf("candidate = %q, want the newest observed value", turn.Candidate)
	}
}
