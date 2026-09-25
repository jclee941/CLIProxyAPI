package main

import (
	"strings"
	"testing"
)

func TestImagePromptCarriesSizeAndQuality(t *testing.T) {
	prompt := imagePromptWithHints(imagesRequest{Prompt: "a lighthouse", Size: "1024x1536", Quality: "high"})

	if !strings.HasPrefix(prompt, "a lighthouse") {
		t.Fatalf("caller prompt lost: %q", prompt)
	}
	if !strings.Contains(prompt, "1024x1536") {
		t.Fatalf("size never reached the prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "high quality") {
		t.Fatalf("quality never reached the prompt: %q", prompt)
	}
}

func TestImagePromptStaysUntouchedWithoutHints(t *testing.T) {
	for _, request := range []imagesRequest{
		{Prompt: "a lighthouse"},
		{Prompt: "a lighthouse", Quality: "auto"},
		{Prompt: "  a lighthouse  "},
	} {
		if got := imagePromptWithHints(request); got != "a lighthouse" {
			t.Fatalf("prompt was rewritten without hints: %q", got)
		}
	}
}
