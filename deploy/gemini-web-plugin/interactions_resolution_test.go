package main

import (
	"bytes"
	"testing"
)

func TestExplicit720pUsesTheMeasuredDefaultOutput(t *testing.T) {
	// Given the resolution that the native video path already produces.
	base := []byte(`{"model":"gemini-omni-1.1-flash","input":"a paper boat"}`)
	explicit := []byte(`{"model":"gemini-omni-1.1-flash","input":"a paper boat","response_format":{"type":"video","resolution":"720p"}}`)
	_, expected, err := parseInteraction(base)
	if err != nil {
		t.Fatal(err)
	}
	// When an SDK caller names that resolution explicitly.
	_, actual, err := parseInteraction(explicit)
	// Then it is accepted without inventing an upstream resolution control.
	if err != nil {
		t.Fatalf("measured 720p output rejected: %v", err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatal("720p changed the default upstream request")
	}
}

func TestUnmeasuredResolutionsRemainUnsupported(t *testing.T) {
	for _, resolution := range []string{"360p", "1080p", "4k"} {
		t.Run(resolution, func(t *testing.T) {
			_, _, err := parseInteraction([]byte(`{"model":"gemini-omni-1.1-flash","input":"a paper boat","response_format":{"type":"video","resolution":"` + resolution + `"}}`))
			if safeCredentialCode(err) != "interaction_resolution_unsupported" {
				t.Fatalf("unmeasured resolution accepted: %v", err)
			}
		})
	}
}

func TestVideoContinuationDoesNotPromiseResolutionConversion(t *testing.T) {
	for _, source := range []string{
		`"input":"continue","previous_interaction_id":"previous"`,
		`"input":[{"type":"text","text":"continue"},{"type":"video","mime_type":"video/mp4","data":"AAAA"}]`,
	} {
		_, _, err := parseInteraction([]byte(`{"model":"gemini-omni-1.1-flash",` + source + `,"response_format":{"resolution":"720p"}}`))
		if safeCredentialCode(err) != "interaction_resolution_inherited" {
			t.Fatalf("source resolution conversion was promised: %v", err)
		}
	}
}
