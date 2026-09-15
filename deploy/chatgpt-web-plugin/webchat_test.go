package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func sseResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestWebChatPromptMarksTheRoles(t *testing.T) {
	prompt, _, err := webChatPrompt([]byte(`{"messages":[
		{"role":"system","content":"be brief"},
		{"role":"user","content":"hello"},
		{"role":"assistant","content":"hi"},
		{"role":"user","content":[{"type":"text","text":"again"}]}]}`))
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	for _, want := range []string{"[System instruction]: be brief", "hello", "[Assistant]: hi", "again"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %q", want, prompt)
		}
	}
	if _, _, err := webChatPrompt([]byte(`{"messages":[{"role":"user","content":"   "}]}`)); err == nil {
		t.Fatal("an empty conversation was accepted")
	}
}

// Tools have to be stated in the prompt and recovered from the reply, because the
// web product has no function calling of its own.
func TestWebChatCarriesToolsBothWays(t *testing.T) {
	payload := []byte(`{"messages":[{"role":"user","content":"weather?"}],
		"tools":[{"type":"function","function":{"name":"get_weather","description":"look up","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}],
		"tool_choice":"required"}`)
	prompt, request, err := webChatPrompt(payload)
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	for _, want := range []string{"# Tool Use", "get_weather", "```tool_call", "MUST call at least one tool"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %q", want, prompt)
		}
	}
	if len(request.Tools) != 1 {
		t.Fatalf("tools = %d", len(request.Tools))
	}

	reply := "```tool_call\n{\"name\": \"get_weather\", \"arguments\": {\"city\": \"Seoul\"}}\n```"
	raw, err := webChatPayload(webChatModel, reply, true)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	rendered := string(raw)
	for _, want := range []string{`"tool_calls"`, "get_weather", `\"city\": \"Seoul\"`, `"finish_reason":"tool_calls"`} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("payload missing %s: %s", want, rendered)
		}
	}
	if strings.Contains(rendered, "```tool_call") {
		t.Fatalf("the raw block leaked into the reply: %s", rendered)
	}
}

// A conversation that already contains a call and its result has to be replayed
// as text, since the product only accepts one prompt.
func TestWebChatReplaysToolHistory(t *testing.T) {
	prompt, _, err := webChatPrompt([]byte(`{"messages":[
		{"role":"user","content":"weather?"},
		{"role":"assistant","content":null,"tool_calls":[{"function":{"name":"get_weather","arguments":"{\"city\":\"Seoul\"}"}}]},
		{"role":"tool","name":"get_weather","content":"{\"celsius\":24}"}]}`))
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	for _, want := range []string{"[Assistant]:", "```tool_call", "get_weather", "[Tool result for get_weather]:", "celsius"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %q", want, prompt)
		}
	}
}

// The stream sends whole message objects in one shape and append patches in
// another; both have to produce the same reply.
func TestWebReadReplyHandlesBothStreamShapes(t *testing.T) {
	whole := strings.Join([]string{
		`data: {"conversation_id":"conv-1","message":{"author":{"role":"user"},"content":{"content_type":"text","parts":["hi"]}}}`,
		`data: {"message":{"author":{"role":"assistant"},"content":{"content_type":"text","parts":["full answer"]}}}`,
		"data: [DONE]",
		"",
	}, "\n")
	text, conversation := webReadReply(sseResponse(whole))
	if text != "full answer" {
		t.Fatalf("text = %q", text)
	}
	if conversation != "conv-1" {
		t.Fatalf("conversation = %q", conversation)
	}

	patches := strings.Join([]string{
		`data: {"conversation_id":"conv-2"}`,
		`data: {"v":"par","o":"append","p":"/message/content/parts/0"}`,
		`data: {"v":"tial","o":"append","p":"/message/content/parts/0"}`,
		"data: [DONE]",
		"",
	}, "\n")
	text, conversation = webReadReply(sseResponse(patches))
	if text != "partial" {
		t.Fatalf("patched text = %q", text)
	}
	if conversation != "conv-2" {
		t.Fatalf("conversation = %q", conversation)
	}
}

// The conversation is keyed by message id rather than ordered, so the newest
// assistant turn has to be chosen by timestamp.
func TestWebLatestAssistantTextPicksTheNewestTurn(t *testing.T) {
	raw := []byte(`{"mapping":{
		"a":{"message":{"author":{"role":"user"},"create_time":3,"content":{"content_type":"text","parts":["question"]}}},
		"b":{"message":{"author":{"role":"assistant"},"create_time":1,"content":{"content_type":"text","parts":["older"]}}},
		"c":{"message":{"author":{"role":"assistant"},"create_time":2,"content":{"content_type":"text","parts":["newer"]}}}}}`)
	if text := webLatestAssistantText(raw); text != "newer" {
		t.Fatalf("text = %q", text)
	}
	if text := webLatestAssistantText([]byte(`{"mapping":{}}`)); text != "" {
		t.Fatalf("empty conversation yielded %q", text)
	}
}

func TestWebChatModelIsRoutedAndRendered(t *testing.T) {
	raw, err := webChatPayload(webChatModel, "answer", false)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	for _, want := range []string{`"chat.completion"`, `"answer"`, webChatModel, `"finish_reason":"stop"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("payload missing %s: %s", want, raw)
		}
	}
	if !claimsWebChatModel("GPT-Web-Chat") {
		t.Fatal("model id is case sensitive")
	}
	if claimsWebChatModel(webImageModel) {
		t.Fatal("the image model was claimed by the chat path")
	}
}
