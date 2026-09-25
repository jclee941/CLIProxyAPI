package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBackgroundStreamingKeepsOfficialEvents(t *testing.T) {
	service, local, entered, release := backgroundFixture(t, &continuationWebFixture{video: true})
	host := service.host
	calls := interactionStreamCalls(service)
	streamHost := service.host
	service.host = func(method string, raw []byte) ([]byte, error) {
		if strings.HasPrefix(method, "host.stream.") {
			return streamHost(method, raw)
		}
		return host(method, raw)
	}
	request := interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","input":"first","background":true,"stream":true}`)
	request.Stream, request.StreamID = true, "background-stream"
	if _, err := service.executeInteraction(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	created := interactionAwait(t, calls)
	if !bytes.HasPrefix(created.Payload, []byte("event: interaction.created\n")) {
		t.Fatalf("missing early receipt: %s", created.Payload)
	}
	interactionAwait(t, entered)
	release()
	for _, event := range []string{"step.start", "step.delta", "step.stop", "interaction.completed", "done"} {
		call := interactionAwait(t, calls)
		if !bytes.HasPrefix(call.Payload, []byte("event: "+event+"\n")) {
			t.Fatalf("event %s: %s", event, call.Payload)
		}
	}
	if call := interactionAwait(t, calls); call.Method != "host.stream.close" || call.Error != "" {
		t.Fatalf("completion: %+v", call)
	}
}

func TestBackgroundPreparedAfterRestartFailsWithoutSubmitting(t *testing.T) {
	service, local := continuationFixture(t)
	id := strings.Repeat("a", 64)
	if err := service.saveContinuations(local, map[string]continuationTurn{continuationKey(id): {
		CallerScope: testCallerScope, Model: omniModel, State: "prepared", Background: true,
	}}); err != nil {
		t.Fatal(err)
	}
	restarted := backgroundRestart(t, service)
	calls := interactionStreamCalls(restarted)
	get := interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","id":"`+id+`","stream":true}`)
	get.Alt, get.Stream, get.StreamID = interactionRetrieveAlt, true, "abandoned"
	if _, err := restarted.executeInteraction(t.Context(), get); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"interaction.created", "interaction.failed", "error"} {
		call := interactionAwait(t, calls)
		if !bytes.HasPrefix(call.Payload, []byte("event: "+event+"\n")) {
			t.Fatalf("event %s: %s", event, call.Payload)
		}
	}
	if call := interactionAwait(t, calls); call.Method != "host.stream.close" || call.Error != "" {
		t.Fatalf("abandoned close: %+v", call)
	}
	get.Stream = false
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
	if failed.Status != "failed" || failed.Error.Code != "background_execution_interrupted" {
		t.Fatalf("abandoned receipt: %+v", failed)
	}
}

func TestBackgroundShutdownLeavesNamedSubmissionRecoverableWithoutResubmit(t *testing.T) {
	fixture := &continuationWebFixture{video: true, pending: true}
	service, local, entered, release := backgroundFixture(t, fixture)
	waiting := make(chan struct{})
	service.continuationWait = func(ctx context.Context) error {
		close(waiting)
		<-ctx.Done()
		return ctx.Err()
	}
	created := backgroundCreate(t, t.Context(), service, interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","input":"first","background":true}`))
	id, operation := backgroundOperation(t, service, created)
	interactionAwait(t, entered)
	release()
	interactionAwait(t, waiting)
	closed := make(chan error, 1)
	go func() { closed <- service.shutdownSessions() }()
	if err := interactionAwait(t, closed); err != nil {
		t.Fatal(err)
	}
	interactionAwait(t, operation.done)
	restarted := backgroundRestart(t, service)
	restarted.client = service.client
	restarted.webOriginOverride, restarted.webRotateOverride = service.webOriginOverride, service.webRotateOverride
	fixture.mu.Lock()
	fixture.pending = false
	fixture.mu.Unlock()
	calls := interactionStreamCalls(restarted)
	get := interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","id":"`+id+`","stream":true}`)
	get.Alt, get.Stream, get.StreamID = interactionRetrieveAlt, true, "recovered"
	if _, err := restarted.executeInteraction(t.Context(), get); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"interaction.created", "step.start", "step.delta", "step.stop", "interaction.completed", "done"} {
		call := interactionAwait(t, calls)
		if !bytes.HasPrefix(call.Payload, []byte("event: "+event+"\n")) {
			t.Fatalf("recovery event %s: %s", event, call.Payload)
		}
	}
	if call := interactionAwait(t, calls); call.Method != "host.stream.close" || call.Error != "" {
		t.Fatalf("recovery close: %+v", call)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 {
		t.Fatalf("restart resubmitted %d turns", len(fixture.fields))
	}
}

func TestBackgroundTerminalContinuationCannotResurrectFromStaleGET(t *testing.T) {
	service, local := continuationFixture(t)
	id := strings.Repeat("a", 64)
	if err := service.saveContinuations(local, map[string]continuationTurn{continuationKey(id): {
		CallerScope: testCallerScope, Model: omniModel, State: "failed", Background: true,
		Conversation: "c_chat", Reply: "r_1", Error: "no_video_generated", ErrorMessage: "no_video_generated",
	}}); err != nil {
		t.Fatal(err)
	}
	// GET can read a pre-terminal snapshot just before the owner exits. Its
	// internal recovery must recheck the terminal state under the account lease.
	request := interactionExecutorRequest(t, local, `{"geminiWebContinuation":{"action":"recover","token":"`+id+`"}}`)
	request.Model, request.Format, request.SourceFormat = omniModel, "gemini", "gemini"
	result, err := service.executeContinuation(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		View continuationView `json:"geminiWebContinuation"`
	}
	if err := json.Unmarshal(result.(continuationResult).Payload, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.View.State != "outcome_unknown" || receipt.View.Error != "no_video_generated" {
		t.Fatalf("terminal recovery: %+v", receipt.View)
	}
}
