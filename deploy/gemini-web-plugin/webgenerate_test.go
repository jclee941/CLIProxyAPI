package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

// The request body is addressed by position, so the slots that carry meaning are
// pinned here: a shifted index changes what the server is asked for.
func TestWebGenerationFieldsPinTheProtocolSlots(t *testing.T) {
	fields := webGenerationFields("hello", 3, 4, "conversation-id", nil)
	if len(fields) != 102 {
		t.Fatalf("field count = %d, want 102", len(fields))
	}
	prompt, ok := fields[0].([]any)
	if !ok || prompt[0] != "hello" {
		t.Fatalf("slot 0 = %#v", fields[0])
	}
	if fields[79] != 3 {
		t.Fatalf("slot 79 (mode) = %#v", fields[79])
	}
	thinking, ok := fields[17].([]any)
	if !ok {
		t.Fatalf("slot 17 = %#v", fields[17])
	}
	inner, ok := thinking[0].([]any)
	if !ok || inner[0] != 4 {
		t.Fatalf("slot 17 depth = %#v", fields[17])
	}
	if fields[59] != "conversation-id" {
		t.Fatalf("slot 59 = %#v", fields[59])
	}
	// An ordinary text turn is not a temporary chat, as the web app sends it.
	if fields[45] != nil {
		t.Fatalf("slot 45 (temporary chat) = %#v, want unset", fields[45])
	}
	for slot, want := range map[int]int{49: 0, 91: 0, 96: 0, 98: 1} {
		if fields[slot] != want {
			t.Fatalf("slot %d = %#v, want %d as the web app sends it", slot, fields[slot], want)
		}
	}
}

// The selection header is the only channel that names the capability, so its
// positions matter as much as the body's.
func TestWebSelectionHeaderCarriesCapabilityAndMode(t *testing.T) {
	encoded := webSelectionHeader(capability{CapabilityID: "cap-42", DisplayName: "3.8 Flash", Mode: 3}, 2, 0, "", "")
	var header []any
	if err := json.Unmarshal([]byte(encoded), &header); err != nil {
		t.Fatalf("decode header: %v", err)
	}
	if len(header) != 15 {
		t.Fatalf("header length = %d, want 15", len(header))
	}
	var extended []any
	if err := json.Unmarshal([]byte(webSelectionHeader(capability{CapabilityID: "cap-42", Mode: 3}, 2, 0, "INSTANCE", "boq_build")), &extended); err != nil {
		t.Fatalf("decode extended header: %v", err)
	}
	if len(extended) != 18 || extended[15] != nil || extended[16] != "INSTANCE" || jsonField(extended[17], 0) != "boq_build" {
		t.Fatalf("instance and build after the mode = %#v", extended)
	}
	var thinking []any
	if err := json.Unmarshal([]byte(webSelectionHeader(capability{CapabilityID: "cap-42", Mode: 3}, 2, 2, "INSTANCE", "boq_build")), &thinking); err != nil {
		t.Fatalf("decode thinking header: %v", err)
	}
	if len(thinking) != 18 || thinking[15] != float64(2) || thinking[16] != "INSTANCE" {
		t.Fatalf("thinking level after the mode = %#v", thinking)
	}
	if header[4] != "cap-42" {
		t.Fatalf("capability id at 4 = %#v", header[4])
	}
	if header[11] != float64(2) {
		t.Fatalf("capacity at 11 = %#v", header[11])
	}
	if header[14] != float64(3) {
		t.Fatalf("mode at 14 = %#v", header[14])
	}
}

func generationFrame(t *testing.T, text string) string {
	t.Helper()
	inner := []any{nil, nil, nil, nil, []any{[]any{nil, []any{text}}}}
	encoded, err := json.Marshal(inner)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := json.Marshal([]any{[]any{"wrb.fr", nil, string(encoded)}})
	if err != nil {
		t.Fatal(err)
	}
	return string(frame)
}

// The stream refines its answer, so the last non-empty value is the reply.
func TestWebReplyTextTakesTheLastRefinement(t *testing.T) {
	stream := fmt.Sprintf("%d\n%s\n%d\n%s\n", 7, generationFrame(t, "partial"), 9, generationFrame(t, "final answer"))
	text, err := webReplyText([]byte(")]}'\n" + stream))
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if text != "final answer" {
		t.Fatalf("text = %q", text)
	}
}

// Code the web app ran comes back fenced with its renderer's info string; the
// reply keeps the language and drops the suffix.
func TestWebReplyTextKeepsRunCodeAsPlainFences(t *testing.T) {
	ran := "```python?code_reference&code_event_index=1\nprint(17 * 23)\n```\n\n```text?code_stdout&code_event_index=1\n391\n```\n\n17 times 23 is 391."
	text, err := webReplyText([]byte(")]}'\n" + generationFrame(t, ran) + "\n"))
	if err != nil || text != "```python\nprint(17 * 23)\n```\n\n```text\n391\n```\n\n17 times 23 is 391." {
		t.Fatalf("text = %q, err = %v", text, err)
	}
}

func TestWebReplyTextRejectsRepliesWithoutContent(t *testing.T) {
	if _, err := webReplyText([]byte(")]}'\n\n")); err == nil {
		t.Fatal("an empty stream was accepted")
	}
	empty, err := json.Marshal([]any{[]any{"wrb.fr", nil, "[]"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := webReplyText([]byte(")]}'\n" + string(empty) + "\n")); err == nil {
		t.Fatal("a frame carrying no text was accepted")
	}
}
