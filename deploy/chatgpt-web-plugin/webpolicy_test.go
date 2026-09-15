package main

import (
	"strings"
	"testing"
)

func TestPolicyRefusalMatchesTheSidecarPhrases(t *testing.T) {
	for _, refusal := range []string{
		"I can't create that image because it may violate our guardrails.",
		"This request was BLOCKED by moderation.",
		"抱歉，我不能生成这张图片。",
		"该请求违反内容政策。",
	} {
		if !webPolicyRefusal(refusal) {
			t.Fatalf("refusal not recognised: %q", refusal)
		}
	}
	for _, benign := range []string{
		"",
		"Here is your image of a red circle on a white background.",
		"생성이 완료되었습니다.",
	} {
		if webPolicyRefusal(benign) {
			t.Fatalf("benign reply treated as a refusal: %q", benign)
		}
	}
}

func TestConversationTextReadsAssistantStringPartsOnly(t *testing.T) {
	document := []byte(`{"mapping":{
		"a":{"message":{"author":{"role":"user"},"content":{"parts":["draw a policy document"]}}},
		"b":{"message":{"author":{"role":"assistant"},"content":{"parts":["Sure, here it is."]}}},
		"c":{"message":{"author":{"role":"assistant"},"content":{"parts":[{"content_type":"image_asset_pointer","asset_pointer":"file-service://file-abc"}]}}},
		"d":{"id":"no-message"}
	}}`)

	text := webConversationText(document)

	if !strings.Contains(text, "Sure, here it is.") {
		t.Fatalf("assistant text missing: %q", text)
	}
	if strings.Contains(text, "draw a policy document") {
		t.Fatalf("user text leaked into the scanned reply: %q", text)
	}
	if strings.Contains(text, "image_asset_pointer") || strings.Contains(text, "file-service") {
		t.Fatalf("a delivered image was scanned as text: %q", text)
	}
	if webPolicyRefusal(text) {
		t.Fatalf("a successful turn was read as a refusal: %q", text)
	}
}

func TestConversationTextSurvivesAnUnreadableDocument(t *testing.T) {
	if got := webConversationText([]byte("not json")); got != "" {
		t.Fatalf("expected no text, got %q", got)
	}
}

func TestRefusedByPolicyOnlyMatchesTheRefusalCode(t *testing.T) {
	if !webRefusedByPolicy(failure(400, webPolicyCode)) {
		t.Fatal("refusal not detected on the credential walk")
	}
	for _, other := range []error{failure(504, "web_image_not_ready"), failure(502, "web_transport_failed")} {
		if webRefusedByPolicy(other) {
			t.Fatalf("unrelated failure stopped the credential walk: %v", other)
		}
	}
}
