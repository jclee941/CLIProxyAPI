package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (send roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return send(request)
}

func clientWith(t *testing.T, send roundTripFunc) *webClient {
	t.Helper()
	client, err := newWebClient("token")
	if err != nil {
		t.Fatal(err)
	}
	client.http = &http.Client{Transport: send}
	return client
}

func textResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}
}

// The shapes a live gpt-5-6-thinking turn at thinking_effort max produced, plus
// a commentary message: hidden system messages, the memory context and the
// reasoning recap are streamed as messages too, and only the final channel is
// the answer.
const thinkingTurn = `event: delta_encoding
data: "v1"
data: {"conversation_id":"conv-t","kind":"topic","token":"tok","type":"resume_conversation_token"}
event: delta
data: {"c":0,"o":"add","p":"","v":{"conversation_id":"conv-t","message":{"author":{"role":"system"},"channel":null,"content":{"content_type":"text","parts":[""]},"recipient":"all","metadata":{"model_slug":"gpt-5-6-thinking","thinking_effort":"max"}}}}
data: {"c":1,"v":{"conversation_id":"conv-t","message":{"author":{"role":"user"},"content":{"content_type":"text","parts":["How many primes?"]},"recipient":"all","metadata":{}}}}
data: {"c":2,"v":{"conversation_id":"conv-t","message":{"author":{"role":"assistant"},"content":{"content_type":"model_editable_context","model_set_context":""},"recipient":"all","metadata":{"model_slug":"gpt-5-6-thinking","thinking_effort":"max"}}}}
data: {"c":3,"v":{"conversation_id":"conv-t","message":{"author":{"role":"assistant"},"content":{"content_type":"reasoning_recap","content":"Thought for 2 seconds"},"recipient":"all","metadata":{"model_slug":"gpt-5-6-thinking","thinking_effort":"max"}}}}
data: {"c":4,"v":{"conversation_id":"conv-t","message":{"author":{"role":"assistant"},"channel":"commentary","content":{"content_type":"text","parts":[""]},"recipient":"all","metadata":{"model_slug":"gpt-5-6-thinking","thinking_effort":"max"}}}}
data: {"p":"/message/content/parts/0","o":"append","v":"Let me count."}
data: {"v":" Still counting."}
data: {"c":5,"v":{"conversation_id":"conv-t","message":{"author":{"role":"assistant"},"channel":"final","content":{"content_type":"text","parts":[""]},"recipient":"all","metadata":{"model_slug":"gpt-5-6-thinking","thinking_effort":"max"}}}}
data: {"o":"patch","v":[{"o":"append","p":"/message/content/parts/0","v":"16"},{"o":"replace","p":"/message/status","v":"finished_successfully"}]}
data: {"conversation_id":"conv-t","type":"message_stream_complete"}
data: [DONE]
`

func TestStreamReplyKeepsOnlyTheFinalAnswerOfAThinkingTurn(t *testing.T) {
	reply, err := webStreamReply(sseResponse(thinkingTurn), nil)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != "16" {
		t.Fatalf("text = %q", reply.Text)
	}
	if reply.Model != webThinkingModel || reply.Effort != "max" {
		t.Fatalf("served = %q %q", reply.Model, reply.Effort)
	}
	if !reply.Complete || reply.ConversationID != "conv-t" {
		t.Fatalf("complete = %v, conversation = %q", reply.Complete, reply.ConversationID)
	}
}

func TestStreamReplyHandsEachGrowthOnAsItArrives(t *testing.T) {
	stream := "data: {\"v\":\"Hel\"}\ndata: {\"v\":\"lo\"}\ndata: [DONE]\n"
	var pieces []string
	reply, err := webStreamReply(sseResponse(stream), func(piece string) error {
		pieces = append(pieces, piece)
		return nil
	})
	if err != nil || reply.Text != "Hello" {
		t.Fatalf("text = %q, err = %v", reply.Text, err)
	}
	if strings.Join(pieces, "|") != "Hel|lo" {
		t.Fatalf("pieces = %q", pieces)
	}
}

func TestStreamReplyStopsReading_whenTheCallerHasLeft(t *testing.T) {
	gone := errors.New("caller left")
	stream := "data: {\"v\":\"Hel\"}\ndata: {\"v\":\"lo\"}\ndata: [DONE]\n"
	reply, err := webStreamReply(sseResponse(stream), func(string) error { return gone })
	if !errors.Is(err, gone) {
		t.Fatalf("err = %v", err)
	}
	if reply.Text != "Hel" || reply.Complete {
		t.Fatalf("read past the departure: %+v", reply)
	}
}

func TestStreamReplyIsIncomplete_whenTheStreamIsCutBeforeItsEnd(t *testing.T) {
	reply, err := webStreamReply(sseResponse("data: {\"conversation_id\":\"conv-c\"}\ndata: {\"v\":\"half\"}\n"), nil)
	if err != nil || reply.Complete {
		t.Fatalf("complete = %v, err = %v", reply.Complete, err)
	}
}

func TestLatestAssistantTextWaitsForAnAnswerStillBeingWritten(t *testing.T) {
	writing := []byte(`{"mapping":{
		"a":{"message":{"author":{"role":"assistant"},"create_time":1,"status":"finished_successfully","content":{"content_type":"text","parts":["older"]}}},
		"b":{"message":{"author":{"role":"assistant"},"create_time":2,"status":"in_progress","channel":"final","content":{"content_type":"text","parts":["newer, still"]}}}}}`)
	if text := webLatestAssistantText(writing); text != "" {
		t.Fatalf("an unfinished answer was returned: %q", text)
	}
	commentary := []byte(`{"mapping":{
		"a":{"message":{"author":{"role":"assistant"},"create_time":1,"status":"finished_successfully","channel":"final","content":{"content_type":"text","parts":["the answer"]}}},
		"b":{"message":{"author":{"role":"assistant"},"create_time":2,"status":"finished_successfully","channel":"commentary","content":{"content_type":"text","parts":["an aside"]}}}}}`)
	if text := webLatestAssistantText(commentary); text != "the answer" {
		t.Fatalf("text = %q", text)
	}
}

func TestFinishReplyCompletesACutStreamFromTheConversation(t *testing.T) {
	var reads []string
	client := clientWith(t, func(request *http.Request) (*http.Response, error) {
		reads = append(reads, request.Method+" "+request.URL.Path)
		return textResponse(request, 200, `{"mapping":{"a":{"message":{"author":{"role":"assistant"},"create_time":1,"status":"finished_successfully","content":{"content_type":"text","parts":["partial answer"]}}}}}`), nil
	})
	var pieces []string
	reply, err := client.finishReply(context.Background(), sseResponse("data: {\"conversation_id\":\"conv-7\"}\ndata: {\"v\":\"par\"}\n"),
		webMode{Model: webThinkingModel, Effort: "max"}, func(piece string) error {
			pieces = append(pieces, piece)
			return nil
		})
	if err != nil || reply.Text != "partial answer" {
		t.Fatalf("text = %q, err = %v", reply.Text, err)
	}
	if strings.Join(pieces, "|") != "par|tial answer" {
		t.Fatalf("pieces = %q", pieces)
	}
	if len(reads) != 1 || reads[0] != "GET /backend-api/conversation/conv-7" {
		t.Fatalf("reads = %q", reads)
	}
}

func TestFinishReplyTrustsAWholeStream(t *testing.T) {
	client := clientWith(t, func(request *http.Request) (*http.Response, error) {
		t.Fatalf("a whole stream was re-read: %s %s", request.Method, request.URL.Path)
		return nil, errors.New("unreachable")
	})
	reply, err := client.finishReply(context.Background(), sseResponse(thinkingTurn), webMode{Model: webThinkingModel, Effort: "max"}, nil)
	if err != nil || reply.Text != "16" {
		t.Fatalf("text = %q, err = %v", reply.Text, err)
	}
}
