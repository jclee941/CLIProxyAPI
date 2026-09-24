package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// The documented extension is prompt syntax rather than a request field, so a
// turn that continues its own conversation extends only when the prompt names
// the previous video. Measured on one account and one conversation, the same
// chain ran 10.005s -> 20.010s -> 30.016s with the declaration written, where
// undeclared follow-ups extended only occasionally.
func TestExtensionNamesThePreviousVideo(t *testing.T) {
	_, payload, err := parseInteraction([]byte(`{"model":"gemini-omni-1.1-flash","input":"keep the shot going","previous_interaction_id":"prev","generation_config":{"video_config":{"task":"extend"}}}`))
	if err != nil {
		t.Fatalf("a documented extend request was refused: %v", err)
	}

	declared, err := withPreviousVideoDeclaration(payload)
	if err != nil {
		t.Fatalf("the declaration was not applied: %v", err)
	}

	prompt := interactionPrompt(t, declared)
	if !strings.HasPrefix(prompt, "[# Sources <PREVIOUS_VIDEO>@Video1] ") {
		t.Fatalf("the previous video was never named: %s", prompt)
	}
	if !strings.HasSuffix(prompt, "keep the shot going") {
		t.Fatalf("the caller's prompt was lost behind the declaration: %s", prompt)
	}
}

// A caller who wrote a role means it. Overwriting one would replace the role
// they asked for with the only role this function knows.
func TestExtensionLeavesAWrittenRoleAlone(t *testing.T) {
	for _, written := range []string{
		"[# Sources <PREVIOUS_VIDEO>@Video1] keep going",
		"[# References <VIDEO_REF_0>@Video1] keep going",
		"<FIRST_FRAME> keep going",
	} {
		t.Run(written, func(t *testing.T) {
			_, payload, err := parseInteraction([]byte(`{"model":"gemini-omni-1.1-flash","input":` + strconv.Quote(written) + `,"previous_interaction_id":"prev","generation_config":{"video_config":{"task":"extend"}}}`))
			if err != nil {
				t.Fatalf("a written role was refused: %v", err)
			}

			declared, err := withPreviousVideoDeclaration(payload)
			if err != nil {
				t.Fatal(err)
			}

			if prompt := interactionPrompt(t, declared); prompt != written {
				t.Fatalf("a written role was rewritten: %s", prompt)
			}
		})
	}
}

// Extend is the only task the product performs here: there is no slot for the
// others, and an uploaded video declared as an edit source was measured to spend
// the whole budget and answer no video.
func TestOnlyTheExtendTaskIsAccepted(t *testing.T) {
	for _, task := range []string{"edit", "generate", "interpolate"} {
		t.Run(task, func(t *testing.T) {
			if _, _, err := parseInteraction([]byte(`{"model":"gemini-omni-1.1-flash","input":"a kite","previous_interaction_id":"prev","generation_config":{"video_config":{"task":"` + task + `"}}}`)); err == nil {
				t.Fatal("a task this surface cannot perform was accepted")
			}
		})
	}
}

// Extension needs a video to continue, and this surface can name two: the
// interaction a previous turn stored, and a clip uploaded with this one.
func TestExtendNeedsAVideoToContinue(t *testing.T) {
	const extend = `,"generation_config":{"video_config":{"task":"extend"}}}`

	if _, _, err := parseInteraction([]byte(`{"model":"gemini-omni-1.1-flash","input":"make it longer"` + extend)); err == nil {
		t.Fatal("an extension with nothing to extend was accepted")
	}
	if _, _, err := parseInteraction([]byte(`{"model":"gemini-omni-1.1-flash","input":[{"type":"text","text":"extend"},{"type":"image","data":"AAAA","mime_type":"image/png"}]` + extend)); err == nil {
		t.Fatal("an image was accepted as the only video extension source")
	}
	if _, _, err := parseInteraction([]byte(`{"model":"gemini-omni-1.1-flash","input":"make it longer","previous_interaction_id":"prev"` + extend)); err != nil {
		t.Fatalf("a previous interaction is a source to extend: %v", err)
	}
	if _, _, err := parseInteraction([]byte(`{"model":"gemini-omni-1.1-flash","input":[{"type":"text","text":"make it longer"},{"type":"video","data":"AAAA","mime_type":"video/mp4"}]` + extend)); err != nil {
		t.Fatalf("an uploaded clip is a source to extend: %v", err)
	}
}

func TestInteractionExtendReachesSameConversationWire(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	first := interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`))
	interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"continue","previous_interaction_id":"`+first+`","generation_config":{"video_config":{"task":"extend"}}}`))
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 2 {
		t.Fatalf("submissions = %d, want 2", len(fixture.fields))
	}
	fields := fixture.fields[1]
	if jsonField(fields[2], 0) != "c_chat" || jsonField(fields[2], 1) != "r_1" {
		t.Fatal("extension lost the parent conversation")
	}
	if jsonField(fields[0], 0) != "[# Sources <PREVIOUS_VIDEO>@Video1] continue" {
		t.Fatalf("extension wire prompt = %v", jsonField(fields[0], 0))
	}
	if jsonField(fields[55], 0, 0) != float64(17) || jsonField(fields[0], 9, 6, 0, 3) != float64(2) {
		t.Fatalf("default portrait framing missing: chip=%v orientation=%v", jsonField(fields[55], 0, 0), jsonField(fields[0], 9, 6, 0, 3))
	}
}

func interactionPrompt(t *testing.T, payload []byte) string {
	t.Helper()
	var content struct {
		Contents []struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(payload, &content); err != nil || len(content.Contents) != 1 || len(content.Contents[0].Parts) == 0 {
		t.Fatalf("unreadable interaction payload: %s", payload)
	}
	return content.Contents[0].Parts[0].Text
}
