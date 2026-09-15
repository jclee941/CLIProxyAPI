package main

import (
	"testing"
)

// The video submission is the text one with a fixed set of overrides; those
// slots are what makes the turn produce a video at all.
func TestWebVideoFieldsApplyTheVideoOverrides(t *testing.T) {
	fields := webVideoFields("a wave", 1, "conversation", webOrientationLandscape)
	if len(fields) != 102 {
		t.Fatalf("field count = %d", len(fields))
	}
	for index, want := range map[int]any{49: 11, 67: 0, 68: 1, 80: 1, 91: 0, 96: 0, 98: 1} {
		if fields[index] != want {
			t.Fatalf("slot %d = %#v, want %#v", index, fields[index], want)
		}
	}
	if fields[45] != nil {
		t.Fatalf("slot 45 = %#v, want nil", fields[45])
	}
	prompt, ok := fields[0].([]any)
	if !ok || len(prompt) != 10 || prompt[0] != "a wave" {
		t.Fatalf("slot 0 = %#v", fields[0])
	}
	thinking, ok := fields[17].([]any)
	if !ok {
		t.Fatalf("slot 17 = %#v", fields[17])
	}
	if depth, ok := thinking[0].([]any); !ok || depth[0] != 0 {
		t.Fatalf("video turns must not think: %#v", fields[17])
	}
	// The framing rides in the video options that hang off the prompt, and slot
	// 55 stays the constant it always was rather than carrying the ratio.
	for ratio, framing := range map[string]int{"16:9": 1, "9:16": 2} {
		framed := webVideoFields("a wave", 1, "conversation", omniOptions{AspectRatio: ratio}.orientation())
		video, ok := jsonField(framed[0], 9, 6, 0).([]any)
		if !ok || len(video) != 4 || video[3] != framing {
			t.Fatalf("%s: video options = %#v, want framing %d", ratio, jsonField(framed[0], 9, 6, 0), framing)
		}
		if jsonField(framed[55], 0, 0) != 16 {
			t.Fatalf("%s: slot 55 = %#v, want the constant it always carried", ratio, framed[55])
		}
	}
}

// The encoder either leaves the index in place or truncates the array and moves
// the value into a trailing map keyed by the one-based index, so both shapes have
// to resolve to the same video.
func videoCandidate(url string, sparse bool) any {
	target := []any{[]any{[]any{[]any{slots(8, map[int]any{7: []any{nil, url}})}}}}
	holder := slots(60, map[int]any{59: target})
	if sparse {
		holder = []any{nil, map[string]any{"60": target}}
	}
	return slots(13, map[int]any{12: holder})
}

func TestWebParseVideoCandidateFindsTheDownload(t *testing.T) {
	for _, sparse := range []bool{false, true} {
		state, err := webParseVideoCandidate(videoCandidate("https://example.invalid/v.mp4", sparse))
		if err != nil {
			t.Fatalf("sparse=%v: %v", sparse, err)
		}
		if !state.Ready || state.URL != "https://example.invalid/v.mp4" {
			t.Fatalf("sparse=%v state = %+v", sparse, state)
		}
	}
}

// While the video is still being produced the reply carries a placeholder chip
// instead of a URL, which must be read as pending rather than as a failure.
func TestWebParseVideoCandidateWaitsOnTheChip(t *testing.T) {
	candidate := slots(13, map[int]any{1: []any{"see https://googleusercontent.com/video_gen_chip/abc"}})
	state, err := webParseVideoCandidate(candidate)
	if err != nil {
		t.Fatalf("pending candidate rejected: %v", err)
	}
	if state.Ready {
		t.Fatalf("state = %+v, want pending", state)
	}
}

func TestWebParseVideoCandidateRejectsAnEmptyReply(t *testing.T) {
	if _, err := webParseVideoCandidate(slots(13, map[int]any{1: []any{"no video here"}})); err == nil {
		t.Fatal("a reply without a video was accepted")
	}
}

func TestWebResponseFramesDecodesPayloads(t *testing.T) {
	frames, err := webResponseFrames([]byte(")]}'\n" + generationFrame(t, "payload") + "\n"))
	if err != nil {
		t.Fatalf("frames: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frames = %d", len(frames))
	}
	if _, err := webResponseFrames([]byte(")]}'\n\n")); err == nil {
		t.Fatal("an empty stream was accepted")
	}
}
