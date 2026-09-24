package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUploadedExtensionUsesSourceRole(t *testing.T) {
	// Given a video uploaded without a caller-written media role.
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call

	// When the official API requests an extension of that upload.
	interactionID(t, interactionCall(t, service, local,
		`{"model":"gemini-omni-1.1-flash","input":[{"type":"video","mime_type":"video/mp4","data":"AAAA"},{"type":"text","text":"Continue the scene"}],"generation_config":{"video_config":{"task":"extend"}}}`))

	// Then the submitted video is a source, not a likeness reference.
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 || len(fixture.uploads) != 1 {
		t.Fatalf("submissions=%d uploads=%d, want one each", len(fixture.fields), len(fixture.uploads))
	}
	prompt, _ := jsonField(fixture.fields[0], 0, 0).(string)
	if !strings.Contains(prompt, "[# Sources <VIDEO_0>@Video1]") || strings.Contains(prompt, "<VIDEO_REF_0>") {
		t.Fatalf("uploaded extension has the wrong media role: %q", prompt)
	}
}

func TestVideoAttachmentDoesNotOverrideSourceFraming(t *testing.T) {
	// Given the same selected portrait chip with either a video or an image.
	for _, mimeType := range []string{"video/mp4", "image/png"} {
		t.Run(mimeType, func(t *testing.T) {
			attachments := []webAttachment{{Path: "/uploaded/media", Name: "media", MIMEType: mimeType}}
			// When the web request is serialized.
			fields := webVideoFields("continue", 1, "nonce", webFramingPortrait, attachments)
			// Then only uploaded video suppresses chip-derived video options.
			orientation := jsonField(fields[0], 9, 6, 0, 3)
			if mimeType == "video/mp4" {
				if orientation != nil {
					t.Fatalf("video source framing overridden by %v", orientation)
				}
			} else if orientation != 2 {
				t.Fatalf("image generation lost portrait orientation: %v", orientation)
			}
			if jsonField(fields[55], 0, 0) != 17 {
				t.Fatal("original selected chip list was changed")
			}
		})
	}
}
