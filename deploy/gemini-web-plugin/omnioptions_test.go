package main

import (
	"errors"
	"strings"
	"testing"
)

func omniRequestWithConfig(config string) []byte {
	return []byte(`{"contents":[{"role":"user","parts":[{"text":"a balloon"}]}],"generationConfig":` + config + `}`)
}

func TestOmniFoldsSupportedOptionsIntoPrompt_andLeavesTheRebuiltRequestUnchanged(t *testing.T) {
	prompt, err := omniPrompt(omniRequestWithConfig(`{"aspectRatio":"9:16","negativePrompt":"text overlays","candidateCount":1}`))
	if err != nil {
		t.Fatalf("supported options rejected: %v", err)
	}
	if !strings.Contains(prompt, "a balloon") || !strings.Contains(prompt, "vertical 9:16") || !strings.Contains(prompt, "Do not include: text overlays") {
		t.Fatalf("options did not reach the prompt: %q", prompt)
	}

	payload, err := omniGeminiPayload(prompt)
	if err != nil {
		t.Fatalf("rebuilt payload rejected: %v", err)
	}
	second, err := omniPrompt(payload)
	if err != nil {
		t.Fatalf("rebuilt payload rejected on re-read: %v", err)
	}
	if second != prompt {
		t.Fatalf("re-reading the rebuilt request changed the prompt:\nfirst:  %q\nsecond: %q", prompt, second)
	}
}

func TestOmniRejectsOptionsTheWebPathCannotHonour_beforeSubmitting(t *testing.T) {
	for _, scenario := range []struct{ name, config, code string }{
		{"duration", `{"durationSeconds":8}`, "omni_unsupported_generation_option"},
		{"resolution", `{"resolution":"1080p"}`, "omni_unsupported_generation_option"},
		{"person_generation", `{"personGeneration":"allow_adult"}`, "omni_unsupported_generation_option"},
		{"unlisted_ratio", `{"aspectRatio":"4:3"}`, "omni_invalid_aspect_ratio"},
		{"ratio_wrong_type", `{"aspectRatio":5}`, "omni_invalid_aspect_ratio"},
		{"negative_wrong_type", `{"negativePrompt":["a"]}`, "omni_invalid_negative_prompt"},
		{"multiple_candidates", `{"candidateCount":2}`, "omni_single_candidate_only"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, err := omniPrompt(omniRequestWithConfig(scenario.config))
			var public *publicError
			if !errors.As(err, &public) {
				t.Fatalf("expected a public rejection, got %v", err)
			}
			if public.HTTPStatus != 400 || public.Code != scenario.code {
				t.Fatalf("expected 400 %s, got %d %s", scenario.code, public.HTTPStatus, public.Code)
			}
		})
	}
}

func TestOmniCountsFoldedDirectivesAgainstThePromptLimit(t *testing.T) {
	long := strings.Repeat("a", 7990)
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"` + long + `"}]}]}`)
	if _, err := omniPrompt(body); err != nil {
		t.Fatalf("prompt within the limit rejected: %v", err)
	}

	withRatio := []byte(`{"contents":[{"role":"user","parts":[{"text":"` + long + `"}]}],"generationConfig":{"aspectRatio":"16:9"}}`)
	_, err := omniPrompt(withRatio)
	var public *publicError
	if !errors.As(err, &public) || public.Code != "omni_prompt_length_invalid" {
		t.Fatalf("folded directive escaped the prompt limit: %v", err)
	}
}
