package main

import "testing"

func TestContinuationFollowupPreparationIsIdempotent(t *testing.T) {
	service, local := continuationFixture(t)
	continuationWeb(t, service, &continuationWebFixture{})
	first := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(first.Token, "first")))
	body := `{"geminiWebContinuation":{"action":"prepare","token":"` + first.Token + `"}}`
	next := continuationReceipt(t, continuationCall(t, service, local, body))
	// When the prepare response is lost and the caller retries.
	replay := continuationReceipt(t, continuationCall(t, service, local, body))
	// Then the same next-turn receipt is returned; another submission cannot be minted.
	if replay.Token != next.Token {
		t.Fatal("prepare minted a second successor")
	}
}
