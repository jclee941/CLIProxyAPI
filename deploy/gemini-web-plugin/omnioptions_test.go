package main

import (
	"errors"
	"strings"
	"testing"
)

func omniRequestWithConfig(config string) []byte {
	return []byte(`{"contents":[{"role":"user","parts":[{"text":"a balloon"}]}],"generationConfig":` + config + `}`)
}

func TestOmniCarriesTheFramingThroughTheRebuiltRequest(t *testing.T) {
	prompt, options, err := omniRequest(omniRequestWithConfig(`{"aspectRatio":"9:16","negativePrompt":"text overlays","candidateCount":1}`))
	if err != nil {
		t.Fatalf("supported options rejected: %v", err)
	}
	if prompt != "a balloon" {
		t.Fatalf("prompt = %q, want the caller's text untouched", prompt)
	}
	if options.framing().orientation != 2 {
		t.Fatalf("framing = %+v, want portrait", options.framing())
	}
	if !strings.Contains(options.applyPrompt(prompt), "Do not include: text overlays") {
		t.Fatalf("negative prompt lost: %q", options.applyPrompt(prompt))
	}

	payload, err := omniGeminiPayload(prompt, options)
	if err != nil {
		t.Fatalf("rebuilt payload rejected: %v", err)
	}
	second, secondOptions, err := omniRequest(payload)
	if err != nil {
		t.Fatalf("rebuilt payload rejected on re-read: %v", err)
	}
	if second != prompt || secondOptions != options {
		t.Fatalf("rebuild lost the request:\nfirst  %q %+v\nsecond %q %+v", prompt, options, second, secondOptions)
	}
}

func TestOmniFramesVerticallyWhenNoneIsAsked(t *testing.T) {
	_, options, err := omniRequest([]byte(`{"contents":[{"role":"user","parts":[{"text":"a balloon"}]}]}`))
	if err != nil {
		t.Fatalf("plain request rejected: %v", err)
	}
	if options.framing() != webFramingPortrait {
		t.Fatalf("framing = %+v, want the vertical default %+v", options.framing(), webFramingPortrait)
	}
}

func TestOmniRejectsOptionsTheWebPathCannotHonour_beforeSubmitting(t *testing.T) {
	for _, scenario := range []struct{ name, config, code string }{
		{"duration", `{"durationSeconds":8}`, "omni_unsupported_generation_option"},
		{"resolution", `{"resolution":"1080p"}`, "omni_unsupported_generation_option"},
		{"person_generation", `{"personGeneration":"allow_adult"}`, "omni_unsupported_generation_option"},
		{"unlisted_ratio", `{"aspectRatio":"4:3"}`, "omni_invalid_aspect_ratio"},
		{"square_ratio", `{"aspectRatio":"1:1"}`, "omni_invalid_aspect_ratio"},
		{"ratio_wrong_type", `{"aspectRatio":5}`, "omni_invalid_aspect_ratio"},
		{"negative_wrong_type", `{"negativePrompt":["a"]}`, "omni_invalid_negative_prompt"},
		{"multiple_candidates", `{"candidateCount":2}`, "omni_single_candidate_only"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, _, err := omniRequest(omniRequestWithConfig(scenario.config))
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

func TestOmniCountsTheNegativePromptAgainstThePromptLimit(t *testing.T) {
	long := strings.Repeat("a", 7990)
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"` + long + `"}]}]}`)
	if _, _, err := omniRequest(body); err != nil {
		t.Fatalf("prompt within the limit rejected: %v", err)
	}

	withNegative := []byte(`{"contents":[{"role":"user","parts":[{"text":"` + long + `"}]}],"generationConfig":{"negativePrompt":"blurry frames and text"}}`)
	_, _, err := omniRequest(withNegative)
	var public *publicError
	if !errors.As(err, &public) || public.Code != "omni_prompt_length_invalid" {
		t.Fatalf("folded negative prompt escaped the prompt limit: %v", err)
	}
}
