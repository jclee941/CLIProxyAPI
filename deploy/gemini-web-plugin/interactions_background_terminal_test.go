package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBackgroundRefusalSurvivesCrashBeforeOutcomePersistence(t *testing.T) {
	// Continuation durably releases a declined turn before the background
	// owner writes its public error. A crash in that gap cannot revive it.
	service, local := continuationFixture(t)
	id := strings.Repeat("a", 64)
	if err := service.saveContinuations(local, map[string]continuationTurn{continuationKey(id): {
		CallerScope: testCallerScope, Model: omniModel, State: "no_video", Background: true,
		Conversation: "c_chat", Reply: "r_1", Candidate: "rc_1",
	}}); err != nil {
		t.Fatal(err)
	}
	restarted := backgroundRestart(t, service)
	get := interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","id":"`+id+`"}`)
	get.Alt = interactionRetrieveAlt
	result, err := restarted.executeInteraction(t.Context(), get)
	if err != nil {
		t.Fatal(err)
	}
	var failed struct {
		Status string
		Error  struct{ Code string }
	}
	if err := json.Unmarshal(result.(continuationResult).Payload, &failed); err != nil {
		t.Fatal(err)
	}
	if failed.Status != "failed" || failed.Error.Code != "no_video_generated" {
		t.Fatalf("durable refusal was resurrected: %+v", failed)
	}
	get.Payload = []byte(`{"geminiWebContinuation":{"action":"recover","token":"` + id + `"}}`)
	get.Model, get.Format, get.SourceFormat, get.Alt = omniModel, "gemini", "gemini", ""
	if _, err := restarted.executeContinuation(t.Context(), get); err != nil {
		t.Fatalf("stale GET cannot recheck refusal locally: %v", err)
	}
}
