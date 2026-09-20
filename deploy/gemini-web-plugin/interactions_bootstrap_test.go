package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type interactionStreamCall struct {
	Method   string
	StreamID string `json:"stream_id"`
	Payload  []byte `json:"payload"`
	Error    string `json:"error"`
}

func interactionStreamCalls(service *service) <-chan interactionStreamCall {
	calls := make(chan interactionStreamCall, 8)
	service.host = func(method string, raw []byte) ([]byte, error) {
		var call interactionStreamCall
		if err := json.Unmarshal(raw, &call); err != nil {
			return nil, err
		}
		call.Method = method
		calls <- call
		return []byte(`{"ok":true,"result":{}}`), nil
	}
	return calls
}

func assertInteractionComment(t *testing.T, payload []byte) {
	t.Helper()
	if !strings.HasSuffix(string(payload), "\n\n") {
		t.Fatalf("unterminated SSE comment: %q", payload)
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(payload), "\n\n"), "\n") {
		if !strings.HasPrefix(line, ":") {
			t.Fatalf("bootstrap must contain only SSE comments, not events or cursors: %q", payload)
		}
	}
}

func TestInteractionResumeBootstrapsBeforeOperationCompletes(t *testing.T) {
	// Given a resumed subscriber joining an operation that has not finished.
	service := newService(nil)
	calls := interactionStreamCalls(service)
	operation := &interactionOperation{done: make(chan struct{}), err: failure(502, "continuation_operation_mismatch")}
	finish := sync.OnceFunc(func() { close(operation.done) })
	t.Cleanup(func() {
		finish()
		if err := service.shutdownSessions(); err != nil {
			t.Error(err)
		}
	})
	// When the subscriber starts, before the operation is allowed to finish.
	if _, err := service.subscribeInteraction("s", "tok", 1, operation); err != nil {
		t.Fatal(err)
	}
	first := interactionAwait(t, calls)
	// Then the host can establish native SSE without replaying event 1.
	if first.Method != "host.stream.emit" || first.StreamID != "s" || first.Error != "" {
		t.Fatalf("bootstrap callback: %+v", first)
	}
	assertInteractionComment(t, first.Payload)
	finish()
	closed := interactionAwait(t, calls)
	if closed.Method != "host.stream.close" || closed.Error != safeCredentialMessage(operation.err) {
		t.Fatalf("original error lost after bootstrap: %+v", closed)
	}
}

func TestInteractionResumeBootstrapsBeforeTerminalClose(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		result string
		err    error
		code   string
	}{
		{"pending", `{"status":"in_progress","steps":[]}`, nil, "interaction_pending_retrieve_receipt"},
		{"denied", "", failure(403, "web_rpc_denied"), "web_rpc_denied"},
		{"mismatch", "", failure(502, "continuation_operation_mismatch"), "continuation_operation_mismatch"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given an already-finished pending or failed recovery operation.
			service := newService(nil)
			calls := interactionStreamCalls(service)
			operation := &interactionOperation{done: make(chan struct{}), result: continuationResult{Payload: []byte(scenario.result)}, err: scenario.err}
			close(operation.done)
			t.Cleanup(func() {
				if err := service.shutdownSessions(); err != nil {
					t.Error(err)
				}
			})
			// When GET resumes after the receipt event.
			if _, err := service.subscribeInteraction("s", "tok", 1, operation); err != nil {
				t.Fatal(err)
			}
			first := interactionAwait(t, calls)
			// Then nonempty native bytes precede close, and the close retains its error.
			if first.Method != "host.stream.emit" {
				t.Fatalf("closed before bootstrap: %+v", first)
			}
			assertInteractionComment(t, first.Payload)
			closed := interactionAwait(t, calls)
			if closed.Method != "host.stream.close" || closed.Error != scenario.code {
				t.Fatalf("terminal callback: %+v", closed)
			}
		})
	}
}

func TestInteractionBootstrapPreservesCompletedEventCursors(t *testing.T) {
	for cursor := 0; cursor <= 6; cursor++ {
		t.Run(fmt.Sprint(cursor), func(t *testing.T) {
			// Given a completed interaction and every supported replay cursor.
			service := newService(nil)
			calls := interactionStreamCalls(service)
			operation := &interactionOperation{done: make(chan struct{}), result: continuationResult{Payload: []byte(`{"status":"completed","steps":[{"content":[{"type":"video","mime_type":"video/mp4","data":"AAAA"}]}]}`)}}
			close(operation.done)
			// When replaying, or creating the initial cursor-zero stream.
			if err := service.sendInteractionEvents("s", "tok", cursor, operation); err != nil {
				t.Fatal(err)
			}
			// Then only resumed streams add one cursor-free comment; all events keep their IDs.
			if cursor > 0 {
				assertInteractionComment(t, interactionAwait(t, calls).Payload)
			}
			names := []string{"interaction.created", "step.start", "step.delta", "step.stop", "interaction.completed", "done"}
			for n := cursor + 1; n <= 6; n++ {
				call := interactionAwait(t, calls)
				prefix := fmt.Sprintf("event: %s\nid: tok:%d\ndata: ", names[n-1], n)
				if call.Method != "host.stream.emit" || !strings.HasPrefix(string(call.Payload), prefix) {
					t.Fatalf("event %d: %+v", n, call)
				}
				data := strings.TrimSuffix(strings.TrimPrefix(string(call.Payload), prefix), "\n\n")
				if n == 6 {
					if data != "[DONE]" {
						t.Fatalf("done: %q", data)
					}
					continue
				}
				var event struct {
					EventType string `json:"event_type"`
					EventID   string `json:"event_id"`
				}
				if err := json.Unmarshal([]byte(data), &event); err != nil {
					t.Fatal(err)
				}
				if event.EventType != names[n-1] || event.EventID != fmt.Sprintf("tok:%d", n) {
					t.Fatalf("event data: %+v", event)
				}
			}
			if len(calls) != 0 {
				t.Fatal("duplicate replay output")
			}
		})
	}
}
