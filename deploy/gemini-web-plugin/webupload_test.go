package main

import (
	"context"
	"encoding/json"
	"testing"
)

// The web product takes a known set of files. Anything else has to be refused
// here, because upstream answers an unsupported upload with a reference error
// that names neither the file nor the reason.
func TestUploadRefusesATypeTheWebProductCannotTake(t *testing.T) {
	session := &webSession{}

	source, err := inlineSource(webMedia{MIMEType: "application/x-msdownload", Data: "AAAA"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := session.uploadSources(context.Background(), []webSource{source}); err == nil {
		t.Fatal("an unsupported attachment type was accepted")
	}
}

func TestAttachmentNoticeNamesWhatWasAttached(t *testing.T) {
	for mimeType, want := range map[string]string{
		"image/png":                 "[Image attached]",
		"application/pdf":           "[Document attached]",
		"text/plain; charset=utf-8": "[Document attached]",
	} {
		if notice := webAttachmentNotice(mimeType); notice != want {
			t.Fatalf("%s was announced as %q, want %q", mimeType, notice, want)
		}
	}
}

func TestAttachmentSlotMatchesTheCapturedWire(t *testing.T) {
	slot := webAttachmentSlot([]webAttachment{{Path: "/contrib_service/ttl_1d/abc", Name: "attach.png", MIMEType: "image/png", ClientID: "fixed-id"}})

	encoded, err := json.Marshal(slot)
	if err != nil {
		t.Fatal(err)
	}
	want := `[[["/contrib_service/ttl_1d/abc",1,null,"image/png","fixed-id"],"attach.png",null,null,null,null,null,null,[0]]]`
	if string(encoded) != want {
		t.Fatalf("attachment slot drifted from the captured wire:\n got %s\nwant %s", encoded, want)
	}
}

func TestAttachmentSlotIsAbsentWithoutFiles(t *testing.T) {
	if slot := webAttachmentSlot(nil); slot != nil {
		t.Fatalf("an empty attachment list still occupied the slot: %v", slot)
	}
}

func TestGenerationFieldsCarryAttachmentsBesideThePrompt(t *testing.T) {
	fields := webGenerationFields("hello", 1, 0, "conversation", []webAttachment{{Path: "/contrib_service/ttl_1d/abc", Name: "a.png", MIMEType: "image/png", ClientID: "id"}})

	prompt, ok := fields[0].([]any)
	if !ok || len(prompt) != 7 {
		t.Fatalf("prompt slot shape changed: %#v", fields[0])
	}
	if prompt[3] == nil {
		t.Fatal("attachments were not placed in slot 3")
	}
}
