package main

import (
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/tidwall/gjson"
)

func TestOpenAIFramesReplayCompletionAsDeltaSequence(t *testing.T) {
	response := []byte(`{"id":"chatcmpl-1","created":123,"model":"gemini-web-flash-3.8","choices":[{"index":0,"message":{"role":"assistant","content":"{\"a\":1}"},"finish_reason":"stop"}],"usage":{"total_tokens":7}}`)
	frames, ok := openAIFrames(response)
	if !ok {
		t.Fatal("completion was not framed")
	}
	events := sseEvents(t, frames)
	if len(events) != 3 {
		t.Fatalf("expected opening, closing and sentinel events, got %d: %q", len(events), events)
	}
	if object := gjson.Get(events[0], "object").String(); object != "chat.completion.chunk" {
		t.Fatalf("opening object = %q", object)
	}
	if content := gjson.Get(events[0], "choices.0.delta.content").String(); content != `{"a":1}` {
		t.Fatalf("opening delta content = %q", content)
	}
	if finish := gjson.Get(events[0], "choices.0.finish_reason"); finish.Type != gjson.Null {
		t.Fatalf("opening finish_reason = %s, want null", finish.Raw)
	}
	if finish := gjson.Get(events[1], "choices.0.finish_reason").String(); finish != "stop" {
		t.Fatalf("closing finish_reason = %q", finish)
	}
	if tokens := gjson.Get(events[1], "usage.total_tokens").Int(); tokens != 7 {
		t.Fatalf("closing usage lost: %s", events[1])
	}
	if events[2] != "[DONE]" {
		t.Fatalf("sentinel = %q", events[2])
	}
	if id := gjson.Get(events[1], "id").String(); id != "chatcmpl-1" {
		t.Fatalf("closing id = %q", id)
	}
}

func TestOpenAIFramesCarryToolCalls(t *testing.T) {
	response := []byte(`{"id":"x","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_0","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Seoul\"}"}}]},"finish_reason":"tool_calls"}]}`)
	frames, ok := openAIFrames(response)
	if !ok {
		t.Fatal("tool call completion was not framed")
	}
	events := sseEvents(t, frames)
	if name := gjson.Get(events[0], "choices.0.delta.tool_calls.0.function.name").String(); name != "get_weather" {
		t.Fatalf("delta tool call = %s", events[0])
	}
	if _, present := gjson.Get(events[0], "choices.0.delta.content").Value().(string); present {
		t.Fatalf("a tool call delta must not carry content: %s", events[0])
	}
	if finish := gjson.Get(events[1], "choices.0.finish_reason").String(); finish != "tool_calls" {
		t.Fatalf("closing finish_reason = %q", finish)
	}
}

func TestGeminiFramesCarryWholeResponse(t *testing.T) {
	response := []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"{\"a\":1}"}]}}]}`)
	frames, ok := geminiFrames(response)
	if !ok {
		t.Fatal("gemini response was not framed")
	}
	events := sseEvents(t, frames)
	if len(events) != 1 {
		t.Fatalf("gemini streams whole responses, got %d events", len(events))
	}
	if text := gjson.Get(events[0], "candidates.0.content.parts.0.text").String(); text != `{"a":1}` {
		t.Fatalf("framed text = %q", text)
	}
}

func TestSSEFramesSelectDialectAndRejectOthers(t *testing.T) {
	openai := []byte(`{"messages":[]}`)
	completion := []byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"{}"}}]}`)
	if _, ok := sseFrames(openai, completion); !ok {
		t.Fatal("openai request must frame a completion")
	}
	gemini := []byte(`{"contents":[]}`)
	generated := []byte(`{"candidates":[{"content":{"parts":[{"text":"{}"}]}}]}`)
	if _, ok := sseFrames(gemini, generated); !ok {
		t.Fatal("gemini request must frame a generateContent response")
	}
	if _, ok := sseFrames([]byte(`{"prompt":"hi"}`), completion); ok {
		t.Fatal("an unknown dialect must not be framed")
	}
}

// Without a host the nested execution cannot run, so the request must fall back
// to the ordinary streaming path instead of terminating with an empty answer.
func TestBufferStrictStreamDegradesWhenHostUnavailable(t *testing.T) {
	req := pluginapi.RequestInterceptRequest{
		SourceFormat: "openai",
		Model:        "gemini-web-flash-3.8",
		Stream:       true,
	}
	instructed := []byte(`{"stream":true,"messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_object"}}`)
	if _, ok := bufferStrictStream(req, testConfig(0), instructed); ok {
		t.Fatal("streaming must not be terminated when the model cannot be reached")
	}
}

func sseEvents(t *testing.T, frames []byte) []string {
	t.Helper()
	trimmed := strings.TrimSuffix(string(frames), "\n\n")
	if trimmed == "" {
		return nil
	}
	blocks := strings.Split(trimmed, "\n\n")
	events := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if !strings.HasPrefix(block, "data: ") {
			t.Fatalf("frame is not an SSE data event: %q", block)
		}
		events = append(events, strings.TrimPrefix(block, "data: "))
	}
	return events
}
