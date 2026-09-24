package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type hostRecorder struct {
	mu     sync.Mutex
	calls  []recordedCall
	refuse bool
}

type recordedCall struct {
	method  string
	payload []byte
	message string
	fields  map[string]any
}

func (recorder *hostRecorder) call(method string, raw []byte) ([]byte, error) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	var request struct {
		Payload []byte         `json:"payload"`
		Error   string         `json:"error"`
		Fields  map[string]any `json:"fields"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	recorder.calls = append(recorder.calls, recordedCall{method: method, payload: request.Payload, message: request.Error, fields: request.Fields})
	switch {
	case method == "host.auth.list":
		return []byte(`{"ok":true,"result":{"files":[{"id":"codex-a","auth_index":"1","provider":"codex"}]}}`), nil
	case recorder.refuse && strings.HasPrefix(method, "host.stream."):
		return []byte(`{"ok":false}`), nil
	default:
		return []byte(`{"ok":true}`), nil
	}
}

func (recorder *hostRecorder) named(method string) []recordedCall {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	var matched []recordedCall
	for _, call := range recorder.calls {
		if call.method == method {
			matched = append(matched, call)
		}
	}
	return matched
}

type sentChunks struct {
	roles        []string
	contents     []string
	calls        []webToolCall
	reasons      []string
	fingerprints []string
}

func (recorder *hostRecorder) chunks(t *testing.T) sentChunks {
	t.Helper()
	var sent sentChunks
	for _, call := range recorder.named("host.stream.emit") {
		var chunk struct {
			Object            string `json:"object"`
			SystemFingerprint string `json:"system_fingerprint"`
			Choices           []struct {
				Delta struct {
					Role      string `json:"role"`
					Content   string `json:"content"`
					ToolCalls []struct {
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(call.payload, &chunk); err != nil || chunk.Object != "chat.completion.chunk" || len(chunk.Choices) != 1 {
			t.Fatalf("not a chat chunk: %s", call.payload)
		}
		if chunk.SystemFingerprint != "" {
			sent.fingerprints = append(sent.fingerprints, chunk.SystemFingerprint)
		}
		choice := chunk.Choices[0]
		if choice.Delta.Role != "" {
			sent.roles = append(sent.roles, choice.Delta.Role)
		}
		if choice.Delta.Content != "" {
			sent.contents = append(sent.contents, choice.Delta.Content)
		}
		for _, call := range choice.Delta.ToolCalls {
			sent.calls = append(sent.calls, webToolCall{Name: call.Function.Name, Arguments: call.Function.Arguments})
		}
		if choice.FinishReason != nil {
			sent.reasons = append(sent.reasons, *choice.FinishReason)
		}
	}
	return sent
}

func relayFor(recorder *hostRecorder, tools bool) *webRelay {
	service := newService(recorder.call)
	return &webRelay{service: service, plan: webChatPlan{model: webChatModel, stream: "7", tools: tools}, id: "chatcmpl-web-test"}
}

func TestRelayStreamsContentAsItArrives_whenNoToolsAreOffered(t *testing.T) {
	recorder := &hostRecorder{}
	relay := relayFor(recorder, false)

	for _, piece := range []string{"Hel", "lo"} {
		if err := relay.deliver(piece); err != nil {
			t.Fatal(err)
		}
	}
	if err := relay.finish("Hello"); err != nil {
		t.Fatal(err)
	}

	sent := recorder.chunks(t)
	if strings.Join(sent.contents, "|") != "Hel|lo" || strings.Join(sent.reasons, "|") != "stop" {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestRelayRunNamesTheServedPresetOnTheClosingChunks(t *testing.T) {
	recorder := &hostRecorder{}
	relay := relayFor(recorder, false)
	relay.client = clientWith(t, func(request *http.Request) (*http.Response, error) {
		return textResponse(request, 200, `{"success":true}`), nil
	})
	ctx, cancel := context.WithCancel(context.Background())

	relay.run(ctx, cancel, sseResponse(thinkingTurn))

	sent := recorder.chunks(t)
	if strings.Join(sent.contents, "") != "16" || len(sent.fingerprints) == 0 {
		t.Fatalf("sent = %+v", sent)
	}
	for _, fingerprint := range sent.fingerprints {
		if fingerprint != "gpt-5-6-thinking/max" {
			t.Fatalf("fingerprints = %q", sent.fingerprints)
		}
	}
}

func TestRelayHoldsACallBlockBackUntilTheEnd_whenToolsAreOffered(t *testing.T) {
	recorder := &hostRecorder{}
	relay := relayFor(recorder, true)
	pieces := []string{"I'll check.\n``", "`tool_call\n{\"name\": \"get_weather\", \"arguments\": {\"city\": \"Seoul\"}}\n```"}

	for _, piece := range pieces {
		if err := relay.deliver(piece); err != nil {
			t.Fatal(err)
		}
	}
	if err := relay.finish(strings.Join(pieces, "")); err != nil {
		t.Fatal(err)
	}

	sent := recorder.chunks(t)
	if strings.Join(sent.contents, "") != "I'll check.\n" {
		t.Fatalf("contents = %q", sent.contents)
	}
	if len(sent.calls) != 1 || sent.calls[0].Name != "get_weather" || !strings.Contains(sent.calls[0].Arguments, "Seoul") {
		t.Fatalf("calls = %+v", sent.calls)
	}
	if strings.Join(sent.reasons, "|") != "tool_calls" {
		t.Fatalf("reasons = %q", sent.reasons)
	}
}

func TestRelayHoldsABareCallBack_whenTheWholeReplyIsTheCall(t *testing.T) {
	recorder := &hostRecorder{}
	relay := relayFor(recorder, true)
	reply := `{"name": "get_weather", "arguments": {}}`

	if err := relay.deliver(reply); err != nil {
		t.Fatal(err)
	}
	if err := relay.finish(reply); err != nil {
		t.Fatal(err)
	}

	sent := recorder.chunks(t)
	if len(sent.contents) != 0 || len(sent.calls) != 1 || strings.Join(sent.reasons, "|") != "tool_calls" {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestRelaySendsHeldTextAsContent_whenItWasNotACall(t *testing.T) {
	recorder := &hostRecorder{}
	relay := relayFor(recorder, true)

	if err := relay.deliver("{not json"); err != nil {
		t.Fatal(err)
	}
	if err := relay.finish("{not json"); err != nil {
		t.Fatal(err)
	}

	sent := recorder.chunks(t)
	if strings.Join(sent.contents, "") != "{not json" || strings.Join(sent.reasons, "|") != "stop" {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestShowableHoldsOnlyWhatCouldStillBeACall(t *testing.T) {
	cases := []struct {
		text, want string
		tools      bool
	}{
		{"plain ```tool_call", "plain ```tool_call", false},
		{"abc`", "abc", true},
		{"abc```tool", "abc", true},
		{"abc```go\nx", "abc```go\nx", true},
		{"  {\"name\"", "", true},
		{"before ```tool_call\n{}", "before ", true},
	}
	for _, c := range cases {
		if got := webShowable(c.text, c.tools); got != c.want {
			t.Fatalf("webShowable(%q, %v) = %q, want %q", c.text, c.tools, got, c.want)
		}
	}
}

func TestRelayRunClosesTheStreamAndDeletesTheConversation(t *testing.T) {
	recorder := &hostRecorder{}
	relay := relayFor(recorder, false)
	var deleted []string
	relay.client = clientWith(t, func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		deleted = append(deleted, request.Method+" "+request.URL.Path+" "+string(body))
		return textResponse(request, 200, `{"success":true}`), nil
	})
	ctx, cancel := context.WithCancel(context.Background())

	relay.run(ctx, cancel, sseResponse("data: {\"conversation_id\":\"conv-r\"}\ndata: {\"v\":\"OK\"}\ndata: [DONE]\n"))

	sent := recorder.chunks(t)
	if strings.Join(sent.roles, "|") != "assistant" || strings.Join(sent.contents, "") != "OK" || strings.Join(sent.reasons, "|") != "stop" {
		t.Fatalf("sent = %+v", sent)
	}
	if closes := recorder.named("host.stream.close"); len(closes) != 1 || closes[0].message != "" {
		t.Fatalf("closes = %+v", closes)
	}
	if len(deleted) != 1 || deleted[0] != `PATCH /backend-api/conversation/conv-r {"is_visible":false}` {
		t.Fatalf("deleted = %q", deleted)
	}
	if ctx.Err() == nil {
		t.Fatal("the turn context outlived the relay")
	}
}

func TestRelayRunStillDeletesTheConversation_whenTheCallerHasLeft(t *testing.T) {
	recorder := &hostRecorder{refuse: true}
	relay := relayFor(recorder, false)
	var deleted []string
	relay.client = clientWith(t, func(request *http.Request) (*http.Response, error) {
		deleted = append(deleted, request.URL.Path)
		return textResponse(request, 200, `{"success":true}`), nil
	})
	ctx, cancel := context.WithCancel(context.Background())

	relay.run(ctx, cancel, sseResponse("data: {\"conversation_id\":\"conv-g\"}\ndata: {\"v\":\"Hello\"}\ndata: [DONE]\n"))

	if len(deleted) != 1 || deleted[0] != "/backend-api/conversation/conv-g" {
		t.Fatalf("deleted = %q", deleted)
	}
}

func TestDiscardReportsACleanupItCouldNotDo(t *testing.T) {
	recorder := &hostRecorder{}
	service := newService(recorder.call)
	client := clientWith(t, func(request *http.Request) (*http.Response, error) {
		return textResponse(request, 500, `{}`), nil
	})

	service.discard(context.Background(), client, "conv-x")

	reports := recorder.named("host.log")
	if len(reports) != 1 || reports[0].fields["error"] != "500 web_conversation_delete_failed" {
		t.Fatalf("reports = %+v", reports)
	}
}

func TestChatStreamNeedsTheHostStreamBridge(t *testing.T) {
	recorder := &hostRecorder{}
	service := newService(recorder.call)
	raw, err := json.Marshal(executorRequest{Model: webChatModel, Payload: []byte(`{"messages":[{"role":"user","content":"hi"}]}`), HostCallbackID: "cb"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.executeChatStream(context.Background(), raw)

	var public *publicError
	if !errors.As(err, &public) || public.Code != "stream_bridge_required" {
		t.Fatalf("err = %v", err)
	}
}
