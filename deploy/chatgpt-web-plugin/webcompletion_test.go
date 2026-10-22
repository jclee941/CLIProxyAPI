package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// This is the termination shape observed on the live 50927 planner replay.
const maxTokensTurn = `data: {"conversation_id":"conv-limit","message":{"author":{"role":"assistant"},"channel":"final","content":{"content_type":"text","parts":["<escape>{\"score\":"]},"metadata":{"model_slug":"gpt-6-pro"}}}
data: {"o":"patch","v":[{"o":"replace","p":"/message/status","v":"finished_partial_completion"},{"o":"append","p":"/message/metadata","v":{"finish_details":{"type":"max_tokens"}}}]}
data: {"conversation_id":"conv-limit","type":"message_stream_complete"}
data: [DONE]
`

func TestWebChatReportsLength_whenUpstreamExhaustsItsOutputBudget(t *testing.T) {
	reply, err := webStreamReply(sseResponse(maxTokensTurn), nil)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := webChatPayload(webProModel, reply, false)

	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) != 1 || response.Choices[0].FinishReason != "length" {
		t.Fatalf("an output limit was reported as successful completion: %s", raw)
	}
}

func TestRelayReportsLength_whenUpstreamExhaustsItsOutputBudget(t *testing.T) {
	recorder := &hostRecorder{}
	relay := relayFor(recorder, false)
	relay.client = clientWith(t, func(request *http.Request) (*http.Response, error) {
		return textResponse(request, 200, `{"success":true}`), nil
	})
	ctx, cancel := context.WithCancel(t.Context())

	relay.run(ctx, cancel, sseResponse(maxTokensTurn))

	if sent := recorder.chunks(t); strings.Join(sent.reasons, "|") != "length" {
		t.Fatalf("an output limit ended as %v", sent.reasons)
	}
}

func TestStructuredRelayRejectsAnOutputLimitWithoutPublishingPartialJSON(t *testing.T) {
	recorder := &hostRecorder{}
	relay := structuredRelayFor(t, recorder)
	relay.client = clientWith(t, func(request *http.Request) (*http.Response, error) {
		return textResponse(request, 200, `{"success":true}`), nil
	})
	ctx, cancel := context.WithCancel(t.Context())

	relay.run(ctx, cancel, sseResponse(maxTokensTurn))

	closed := recorder.named("host.stream.close")
	if len(closed) != 1 || closed[0].message != "web_structured_output_truncated" {
		t.Fatalf("an output limit lost its cause: %+v", closed)
	}
	if sent := recorder.chunks(t); len(sent.contents) != 0 || len(sent.reasons) != 0 {
		t.Fatalf("partial JSON escaped: %+v", sent)
	}
}

func TestFinishReplyPreservesAnOutputLimitFromTheStoredConversation(t *testing.T) {
	client := clientWith(t, func(request *http.Request) (*http.Response, error) {
		return textResponse(request, 200, `{"mapping":{"a":{"message":{"author":{"role":"assistant"},"create_time":1,"status":"finished_partial_completion","content":{"content_type":"text","parts":["partial"]},"metadata":{"finish_details":{"type":"max_tokens"}}}}}}`), nil
	})

	reply, err := client.finishReply(t.Context(), sseResponse("data: {\"conversation_id\":\"conv-limit\"}\ndata: {\"v\":\"par\"}\n"), webMode{Model: webProModel}, nil)

	if err != nil || reply.Text != "partial" || reply.finishReason() != "length" {
		t.Fatalf("stored completion lost its output limit: reply=%+v err=%v", reply, err)
	}
}
