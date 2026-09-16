package main

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

// A cut stream has to keep reading as the same failure every caller already
// handles, and has to keep what did arrive: the count of delivered frames is
// what separates a turn recovery can still find from one that was never named.
func TestACutStreamKeepsItsPublicCodeAndWhatArrived(t *testing.T) {
	delivered := `[["di",1]]` + "\n"
	reader := io.MultiReader(strings.NewReader(delivered), iotest.ErrReader(io.ErrUnexpectedEOF))

	_, err := readContinuationStream(reader, func([]byte) error { return nil })

	if safeCredentialCode(err) != "web_response_failed" {
		t.Fatalf("code = %q, want the failure callers already handle", safeCredentialCode(err))
	}
	var cut *webStreamCut
	if !errors.As(err, &cut) {
		t.Fatalf("cut detail lost: %v", err)
	}
	if string(cut.Delivered) != delivered {
		t.Fatalf("delivered = %q, want the bytes that arrived before the cut", cut.Delivered)
	}
	if !errors.Is(cut.Cause, io.ErrUnexpectedEOF) {
		t.Fatalf("cause = %v, want the transport error that ended the stream", cut.Cause)
	}
}

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
