package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestContinuationObserverIgnoresNonPayloadFrames(t *testing.T) {
	// Given Google's stream bookkeeping alongside a receipt payload.
	frame := slots(26, map[int]any{1: []any{"c_chat", "r_turn"}, 4: []any{[]any{"rc_candidate"}}, 25: "context"})
	raw := `[["di",123]]` + "\n" + string(jsonFixture(t, []any{[]any{"wrb.fr", nil, string(jsonFixture(t, frame))}})) + "\n" + `[["af.httprm",123]]` + "\n"
	turn := continuationTurn{}
	// When complete lines are observed without waiting for stream completion.
	_, err := readContinuationStream(strings.NewReader(raw), func(line []byte) error {
		updated, err := continuationFrame(turn, line)
		if err == nil {
			turn = updated
		}
		return err
	})
	// Then housekeeping does not discard the bound receipt.
	if err != nil {
		t.Fatal(err)
	}
	if turn.Conversation != "c_chat" || turn.Reply != "r_turn" || turn.Candidate != "rc_candidate" {
		t.Fatalf("receipt: %+v", turn)
	}
	var metadata []any
	if err := json.Unmarshal([]byte(turn.Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata[9] != "context" {
		t.Fatal("context slot lost")
	}
}
