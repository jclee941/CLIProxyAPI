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
	for _, want := range []string{"get_weather", "```tool_call", `"city":{"type":"string"}`} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %q", want, prompt)
		}
	}
	if len(request.Tools) != 1 {
		t.Fatalf("tools = %d", len(request.Tools))
	}
	optional, _, err := webChatPrompt([]byte(strings.Replace(string(payload), `"required"`, `"auto"`, 1)))
	if err != nil || optional == prompt {
		t.Fatalf("tool_choice required left the prompt unchanged, err = %v", err)
	}

	reply := "```tool_call\n{\"name\": \"get_weather\", \"arguments\": {\"city\": \"Seoul\"}}\n```"
	raw, err := webChatPayload(webChatModel, webReply{Text: reply}, true)
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
	reply, err := webStreamReply(sseResponse(whole), nil)
	if err != nil || reply.Text != "full answer" {
		t.Fatalf("text = %q, err = %v", reply.Text, err)
	}
	if reply.ConversationID != "conv-1" {
		t.Fatalf("conversation = %q", reply.ConversationID)
	}

	patches := strings.Join([]string{
		`data: {"conversation_id":"conv-2"}`,
		`data: {"v":"par","o":"append","p":"/message/content/parts/0"}`,
		`data: {"v":"tial","o":"append","p":"/message/content/parts/0"}`,
		"data: [DONE]",
		"",
	}, "\n")
	reply, err = webStreamReply(sseResponse(patches), nil)
	if err != nil || reply.Text != "partial" {
		t.Fatalf("patched text = %q, err = %v", reply.Text, err)
	}
	if reply.ConversationID != "conv-2" {
		t.Fatalf("conversation = %q", reply.ConversationID)
	}
}

// These are the event shapes a real turn produced. Handling only the flat ones
// truncated the reply at the first nested delta, so every observed shape is
// pinned here.
func TestWebReadReplyFoldsEveryObservedPatchShape(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"conversation_id":"conv-9","type":"message_stream_start"}`,
		`data: {"v":"Hello"}`,
		`data: {"o":"append","p":"/message/content/parts/0","v":", world"}`,
		`data: {"c":1,"v":{"p":"/message/content/parts/0","o":"append","v":"! 1"}}`,
		`data: {"c":2,"v":{"v":", 2"}}`,
		`data: {"o":"patch","v":[{"p":"/message/content/parts/0","o":"append","v":", 3"},{"p":"/message/content/parts/0","o":"append","v":", DONE"}]}`,
		"data: [DONE]",
		"",
	}, "\n")
	reply, err := webStreamReply(sseResponse(stream), nil)
	if err != nil || reply.Text != "Hello, world! 1, 2, 3, DONE" {
		t.Fatalf("text = %q, err = %v", reply.Text, err)
	}
	if reply.ConversationID != "conv-9" {
		t.Fatalf("conversation = %q", reply.ConversationID)
	}
}

// A metadata string addressed at some other path must not land in the reply.
func TestWebReadReplyIgnoresUnrelatedPaths(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"o":"append","p":"/message/metadata/title","v":"a title"}`,
		`data: {"o":"append","p":"/message/content/parts/0","v":"real"}`,
		"data: [DONE]",
		"",
	}, "\n")
	if reply, err := webStreamReply(sseResponse(stream), nil); err != nil || reply.Text != "real" {
		t.Fatalf("text = %q, err = %v", reply.Text, err)
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
	raw, err := webChatPayload(webChatModel, webReply{Text: "answer"}, false)
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

func TestWebChatExposesGpt6ProOnTheWebSession(t *testing.T) {
	ids := map[string]bool{}
	for _, model := range webChatModels() {
		ids[model.ID] = true
	}
	if !ids[webProModel] {
		t.Fatal("gpt-6-pro is missing from the web chat catalog")
	}
	if !claimsWebChatModel("GPT-6-Pro") {
		t.Fatal("gpt-6-pro must be claimed by the web chat path")
	}
	if mode, err := webChatMode(webProModel, webChatRequest{}); err != nil || mode.Model != webProModel {
		t.Fatalf("pro mode = %+v, err = %v", mode, err)
	}
	if mode, err := webChatMode(webChatModel, webChatRequest{}); err != nil || mode.Model != webUpstreamModel || mode.Effort != "" {
		t.Fatalf("default chat must stay auto, got %+v, err = %v", mode, err)
	}
}

func TestChatGPTProCredentialsAreTriedFirst(t *testing.T) {
	entries := []hostEntry{
		{ID: "codex-aaa-user@example.com-prolite", Name: "user@example.com-prolite"},
		{ID: "codex-bbb-jclee@jclee.me-pro", Name: "jclee@jclee.me-pro"},
		{ID: "codex-ccc-other@example.com", Name: "other@example.com"},
	}
	ordered := preferProCredentials(entries)
	if ordered[0].ID != "codex-bbb-jclee@jclee.me-pro" {
		t.Fatalf("first = %q", ordered[0].ID)
	}
	if isChatGPTProCredential(entries[0]) {
		t.Fatal("prolite was treated as Pro")
	}
	if !isChatGPTProCredential(entries[1]) {
		t.Fatal("the Pro credential was missed")
	}
}
