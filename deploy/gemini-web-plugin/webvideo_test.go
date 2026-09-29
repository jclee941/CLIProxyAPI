package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// An attached video is uploaded, accepted and then ignored unless the prompt
// declares it as a reference, which the generation reports as no_video_generated.
func TestVideoAttachmentIsDeclaredAsAReference(t *testing.T) {
	video := []webAttachment{{MIMEType: "video/mp4"}}

	declared := webReferenceDeclaration("a dog runs", video)

	if !strings.Contains(declared, "[# References <VIDEO_REF_0>@Video1]") || !strings.Contains(declared, "a dog runs") {
		t.Fatalf("the video was not declared as a reference: %q", declared)
	}
	if image := webReferenceDeclaration("a dog runs", []webAttachment{{MIMEType: "image/png"}}); image != "a dog runs" {
		t.Fatalf("an image prompt was rewritten: %q", image)
	}
	for _, own := range []string{
		"[# References <VIDEO_REF_0>@Video1] the dog in <VIDEO_REF_0> runs",
		"[# Sources <VIDEO_0>@Video1] extend this",
	} {
		if again := webReferenceDeclaration(own, video); again != own {
			t.Fatalf("a caller's own declaration was doubled: %q", again)
		}
	}
	if none := webReferenceDeclaration("a dog runs", nil); none != "a dog runs" {
		t.Fatalf("a prompt with no attachment was rewritten: %q", none)
	}
}

// The video submission is the text one with a fixed set of overrides; those
// slots are what makes the turn produce a video at all.
func TestWebVideoFieldsApplyTheVideoOverrides(t *testing.T) {
	fields := webVideoFields("a wave", 1, "conversation", webFramingLandscape, nil)
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
	// The chip in slot 55 and the orientation inside the turn state the same
	// framing, and a request that lets them disagree stalls the upstream.
	for ratio, want := range map[string]omniFraming{"16:9": {16, 1}, "9:16": {17, 2}} {
		framed := webVideoFields("a wave", 1, "conversation", omniOptions{AspectRatio: ratio}.framing(), nil)
		video, ok := jsonField(framed[0], 9, 6, 0).([]any)
		if !ok || len(video) != 4 || video[3] != want.orientation {
			t.Fatalf("%s: video options = %#v, want orientation %d", ratio, jsonField(framed[0], 9, 6, 0), want.orientation)
		}
		if jsonField(framed[55], 0, 0) != want.chip {
			t.Fatalf("%s: slot 55 = %#v, want chip %d", ratio, framed[55], want.chip)
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

// An account on the asynchronous video flow closes the stream once the video is
// accepted, and until it is ready the reply has no text and no chip: only its
// status says the turn is still running. Both candidates are what a live account
// returned on 2026-09-26, trimmed; the second is a finished reply without a video.
func TestWebParseVideoCandidateWaitsOnAnAsynchronousVideo(t *testing.T) {
	decode := func(raw string) any {
		var candidate any
		if err := json.Unmarshal([]byte(raw), &candidate); err != nil {
			t.Fatal(err)
		}
		return candidate
	}
	state, err := webParseVideoCandidate(decode(`["rc_d3a849f1d97af68a",[""],null,null,null,null,null,null,[1],null,null,null,[null,null,null,null,null,null,null,[]]]`))
	if err != nil || state.Ready {
		t.Fatalf("an accepted video still in progress read as state=%+v err=%v", state, err)
	}
	_, err = webParseVideoCandidate(decode(`["rc_200f6bba86f3d3b5",["저는 언어 모델일 뿐이라서 그것을 도와드릴 수가 없습니다."],null,null,null,null,null,null,[2],"ko"]`))
	if safeCredentialCode(err) != "no_video_generated" {
		t.Fatalf("a finished reply without a video answered %v", err)
	}
}

// A declined turn answers in prose, and that prose is the only thing telling a
// spent video allowance apart from a refused prompt, so it has to reach the
// caller instead of being replaced by the bare code.
func TestWebParseVideoCandidateCarriesTheRefusalText(t *testing.T) {
	spoken := "You've reached your daily limit for\n  video generation."
	_, err := webParseVideoCandidate(slots(13, map[int]any{1: []any{spoken}}))
	var public *publicError
	if !errors.As(err, &public) {
		t.Fatalf("a reply without a video answered %v", err)
	}
	if public.Code != "no_video_generated" || public.HTTPStatus != 422 {
		t.Fatalf("code = %d %s, want 422 no_video_generated", public.HTTPStatus, public.Code)
	}
	if !strings.Contains(public.Message, "daily limit for video generation.") {
		t.Fatalf("message = %q, want the reply flattened into it", public.Message)
	}
	long, err := webParseVideoCandidate(slots(13, map[int]any{1: []any{strings.Repeat("가", 900)}}))
	if errors.As(err, &public); len([]rune(public.Message)) > 450 {
		t.Fatalf("an unbounded reply reached the message: %d runes (%+v)", len([]rune(public.Message)), long)
	}
	if _, err := webParseVideoCandidate(slots(13, nil)); err == nil {
		t.Fatal("a reply with no text at all was accepted")
	}
}

// The prefix is what the host's stop rules match, and they match the message
// rather than the code, so carrying the refusal text must not displace it.
func TestExecutionFailureKeepsThePrefixTheHostMatches(t *testing.T) {
	wrapped := executionFailure(omniModel, webNoVideo("no video today"))
	var public *publicError
	if !errors.As(wrapped, &public) {
		t.Fatalf("wrapped = %v", wrapped)
	}
	if !strings.HasPrefix(wrapped.Error(), "gemini_web_omni:") {
		t.Fatalf("the host matches %q, which no longer carries the prefix", wrapped.Error())
	}
	if !strings.Contains(wrapped.Error(), "no video today") {
		t.Fatalf("the reply was dropped: %q", wrapped.Error())
	}
	if public.Code != "gemini_web_omni:no_video_generated" || public.HTTPStatus != 422 {
		t.Fatalf("code = %d %s", public.HTTPStatus, public.Code)
	}
	plain := executionFailure(omniModel, failure(409, "session_busy"))
	if plain.Error() != "gemini_web_omni:session_busy" {
		t.Fatalf("an ordinary failure changed shape: %q", plain.Error())
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
