package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestInteractionGETEndedReceiptIsTerminalWithoutRecovery(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		stream bool
		cursor int
	}{
		{"snapshot", false, 0},
		{"initial", true, 0},
		{"resume", true, 1},
		{"failed-replay", true, 2},
		{"invalid-3", true, 3},
		{"invalid-4", true, 4},
		{"invalid-5", true, 5},
		{"invalid-6", true, 6},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given a ready account with an ended, named turn but no active key
			// or stored result. The fixture rejects every upstream request.
			service, local := continuationFixture(t)
			token := strings.Repeat("a", 64)
			turn := continuationTurn{
				CallerScope: testCallerScope, Model: omniModel, State: "no_operation",
				Conversation: "c_chat", Reply: "r_1", Candidate: "rc_1",
			}
			if err := service.saveContinuations(local, map[string]continuationTurn{continuationKey(token): turn}); err != nil {
				t.Fatal(err)
			}
			calls := interactionStreamCalls(service)
			t.Cleanup(func() {
				if err := service.shutdownSessions(); err != nil {
					t.Error(err)
				}
			})
			cursor := ""
			if scenario.cursor > 0 {
				cursor = fmt.Sprintf("%s:%d", token, scenario.cursor)
			}
			body := fmt.Sprintf(`{"model":%q,"id":%q,"stream":%t,"last_event_id":%q}`, interactionOmniModel, token, scenario.stream, cursor)
			request := interactionExecutorRequest(t, local, body)
			request.Alt, request.Stream, request.StreamID = interactionRetrieveAlt, scenario.stream, "s"
			// When GET retrieves the stale terminal receipt, with old chat handles intact.
			result, err := service.executeInteraction(t.Context(), request)
			// Then it cannot resurrect the turn, contact history, or submit again.
			if scenario.cursor > 2 {
				if safeCredentialCode(err) != "invalid_interaction_event_id" {
					t.Fatalf("accepted impossible terminal cursor: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var payload []byte
			if scenario.stream {
				first := interactionAwait(t, calls)
				if scenario.cursor > 0 {
					assertInteractionComment(t, first.Payload)
				} else if !strings.HasPrefix(string(first.Payload), "event: interaction.created\nid: "+token+":1\n") {
					t.Fatalf("missing receipt: %s", first.Payload)
				}
				if scenario.cursor < 2 {
					failed := interactionAwait(t, calls)
					prefix := "event: interaction.failed\nid: " + token + ":2\ndata: "
					if failed.Method != "host.stream.emit" || failed.Error != "" || !strings.HasPrefix(string(failed.Payload), prefix) {
						t.Fatalf("missing terminal interaction: %+v", failed)
					}
					var event struct{ Interaction json.RawMessage }
					if err := json.Unmarshal([]byte(strings.TrimPrefix(string(failed.Payload), prefix)), &event); err != nil {
						t.Fatal(err)
					}
					payload = event.Interaction
				}
				terminal := interactionAwait(t, calls)
				const prefix = "event: error\ndata: "
				var signal struct {
					Error struct{ Message, Type, Code string }
				}
				if err := json.Unmarshal([]byte(strings.TrimPrefix(string(terminal.Payload), prefix)), &signal); err != nil {
					t.Fatal(err)
				}
				if terminal.Method != "host.stream.emit" || terminal.Error != "" || signal.Error.Message != "missing_upstream_operation" || signal.Error.Type != "server_error" || signal.Error.Code != "internal_server_error" {
					t.Fatalf("terminal error contract lost: %+v %+v", terminal, signal)
				}
				if closed := interactionAwait(t, calls); closed.Method != "host.stream.close" || closed.Error != "" || len(calls) != 0 {
					t.Fatalf("terminal outcome became credential failure or duplicated events: %+v", closed)
				}
			} else {
				payload = result.(continuationResult).Payload
			}
			if scenario.cursor < 2 {
				var outcome struct {
					ID, Status string
					Steps      []json.RawMessage
					Error      struct{ Code, Message string }
				}
				if err := json.Unmarshal(payload, &outcome); err != nil {
					t.Fatal(err)
				}
				if outcome.ID != token || outcome.Status != "failed" || len(outcome.Steps) != 0 || outcome.Error.Code != "missing_upstream_operation" || outcome.Error.Message != "missing_upstream_operation" {
					t.Fatalf("ended receipt is not terminal: %s", payload)
				}
			}
		})
	}
}
