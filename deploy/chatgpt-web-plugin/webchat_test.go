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
	prompt, err := webChatPrompt([]byte(`{"messages":[
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
	if _, err := webChatPrompt([]byte(`{"messages":[{"role":"user","content":"   "}]}`)); err == nil {
		t.Fatal("an empty conversation was accepted")
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
	raw, err := webChatPayload(webChatModel, "answer")
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
