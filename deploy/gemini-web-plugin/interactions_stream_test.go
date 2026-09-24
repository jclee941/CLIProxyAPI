package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// A turn the product declined is not an internal fault and will never succeed
// on a retry, so the outcome reaches the caller where it survives: the
// interaction's own status field, then the error event. The close itself must
// carry nothing. The host records a close that carries an error against the
// credential, and the sixty second cooldown that followed every declined turn
// refused the caller's retry of its chain with continuation_account_unavailable.
func TestDeclinedTurnIsStatedOnTheStreamAndClosesWithoutCoolingTheAccount(t *testing.T) {
	service := newService(nil)
	calls := interactionStreamCalls(service)
	operation := &interactionOperation{done: make(chan struct{}), err: webNoVideo("Daily video limit reached.")}
	close(operation.done)
	t.Cleanup(func() {
		if err := service.shutdownSessions(); err != nil {
			t.Error(err)
		}
	})

	if _, err := service.subscribeInteraction("s", "tok", 0, operation); err != nil {
		t.Fatal(err)
	}

	var emitted [][]byte
	for {
		call := interactionAwait(t, calls)
		if call.Method == "host.stream.close" {
			if call.Error != "" {
				t.Fatalf("the close carried the refusal, which cools the account: %q", call.Error)
			}
			break
		}
		emitted = append(emitted, call.Payload)
	}
	joined := string(bytes.Join(emitted, []byte("\n")))
	if !strings.Contains(joined, "event: interaction.failed") || !strings.Contains(joined, `"status":"failed"`) {
		t.Fatalf("no failed interaction was stated on the stream: %s", joined)
	}
	last := string(emitted[len(emitted)-1])
	if !strings.HasPrefix(last, "event: error\ndata: ") || !strings.Contains(last, "no_video_generated: Daily video limit reached.") {
		t.Fatalf("the error event a subscriber waits for did not follow the failure: %s", last)
	}
}

func TestInteractionStreamingAcceptedBeforeGeneration(t *testing.T) {
	// Given an authenticated official Interactions request.
	service, _ := continuationFixture(t)
	// When streaming is requested, the interceptor must not reject the route.
	response := service.interceptContinuation(jsonFixture(t, map[string]any{"SourceFormat": "interactions", "Model": interactionOmniModel, "Stream": true, "Body": []byte(`{"model":"gemini-omni-1.1-flash","input":"video","stream":true,"store":true}`), "Metadata": map[string]string{"caller_scope": testCallerScope}}))
	// Then preparation and asynchronous execution can be reached.
	if response.Terminate {
		t.Fatalf("stream rejected: %s", response.ResponseBody)
	}
	if _, _, err := parseInteraction([]byte(`{"model":"gemini-omni-1.1-flash","input":"video","stream":true}`)); err != nil {
		t.Fatal(err)
	}
}

func interactionExecutorRequest(t *testing.T, local localSession, body string) executorRequest {
	t.Helper()
	auth, err := authFromRecord(local.Target)
	if err != nil {
		t.Fatal(err)
	}
	request := executorRequest{AuthID: local.Target.ID, AuthProvider: provider, Model: interactionOmniModel, Format: "interactions", SourceFormat: "interactions", StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, Payload: []byte(body), HostCallbackID: "fixture-generation"}
	request.Metadata.CallerScope = testCallerScope
	return request
}

func interactionAwait[T any](t *testing.T, signal <-chan T) T {
	t.Helper()
	select {
	case value := <-signal:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("interaction signal timed out")
		var zero T
		return zero
	}
}

func TestInteractionStreamDisconnectRecoversExactlyOneSubmission(t *testing.T) {
	// Given a real upstream HTTP fixture blocked at its submission boundary.
	service, local := continuationFixture(t)
	submitting, release := make(chan struct{}), make(chan struct{})
	fixture := &continuationWebFixture{video: true, beforeSubmit: func() { close(submitting); <-release }}
	continuationWeb(t, service, fixture)
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		if err := service.shutdownSessions(); err != nil {
			t.Error(err)
		}
	})
	events, closed := make(chan []byte, 8), make(chan struct{}, 1)
	host := &loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}
	service.host = func(method string, raw []byte) ([]byte, error) {
		switch method {
		case "host.stream.emit":
			var event struct {
				Payload []byte `json:"payload"`
			}
			if err := json.Unmarshal(raw, &event); err != nil {
				return nil, err
			}
			events <- event.Payload
			return []byte(`{"ok":true,"result":{}}`), nil
		case "host.stream.close":
			closed <- struct{}{}
			return []byte(`{"ok":true,"result":{}}`), nil
		default:
			return host.call(method, raw)
		}
	}
	request := interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","input":"first","stream":true,"store":true}`)
	request.Stream, request.StreamID = true, "subscriber-1"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// When POST returns before upstream completes and its subscriber disconnects.
	if _, err := service.executeInteraction(ctx, request); err != nil {
		t.Fatal(err)
	}
	first := interactionAwait(t, events)
	var created struct {
		Interaction struct {
			ID string `json:"id"`
		} `json:"interaction"`
		EventType string `json:"event_type"`
	}
	data := strings.SplitN(string(first), "data: ", 2)
	if len(data) != 2 || json.Unmarshal([]byte(strings.TrimSpace(data[1])), &created) != nil || created.EventType != "interaction.created" {
		t.Fatalf("created: %s", first)
	}
	token := created.Interaction.ID
	stored, err := service.sessions.read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	turns, err := continuationTurns(stored)
	if err != nil || turns[continuationKey(token)].CallerScope != testCallerScope {
		t.Fatal("created was not durable and caller-bound")
	}
	interactionAwait(t, submitting)
	cancel()
	get := interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","id":"`+token+`"}`)
	get.Alt = interactionRetrieveAlt
	pending, err := service.executeInteraction(t.Context(), get)
	if err != nil || !strings.Contains(string(pending.(continuationResult).Payload), `"in_progress"`) {
		t.Fatalf("pending GET: %v", err)
	}
	releaseOnce.Do(func() { close(release) })
	interactionAwait(t, closed)
	// Then completion is durable and GET/cursor replay never generates again.
	got, err := service.executeInteraction(t.Context(), get)
	if err != nil || !strings.Contains(string(got.(continuationResult).Payload), `"completed"`) {
		t.Fatalf("completed GET: %v", err)
	}
	for n := 2; n <= 6; n++ {
		event := interactionAwait(t, events)
		if !strings.Contains(string(event), "id: "+token+":"+string(rune('0'+n))) {
			t.Fatalf("event %d: %s", n, event)
		}
	}
	get.Stream, get.StreamID = true, "subscriber-2"
	get.Payload = []byte(`{"model":"gemini-omni-1.1-flash","id":"` + token + `","stream":true,"last_event_id":"` + token + `:3"}`)
	if _, err := service.executeInteraction(t.Context(), get); err != nil {
		t.Fatal(err)
	}
	interactionAwait(t, closed)
	assertInteractionComment(t, interactionAwait(t, events))
	for _, name := range []string{"step.stop", "interaction.completed", "done"} {
		if event := interactionAwait(t, events); !strings.HasPrefix(string(event), "event: "+name+"\n") {
			t.Fatalf("replay: %s", event)
		}
	}
	get.Stream = false
	get.Metadata.CallerScope = strings.Repeat("d", 64)
	if _, err := service.executeInteraction(t.Context(), get); err == nil {
		t.Fatal("wrong caller retrieved video")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 {
		t.Fatalf("submissions: %d", len(fixture.fields))
	}
}
