package main

import (
	"encoding/json"
	"strings"
)

// webOrientationLandscape is the framing the web app sends when the user leaves
// the control alone, and the one the video payload has always carried.
const webOrientationLandscape = 1

// omniOrientations maps the accepted ratios onto the framing the video options
// carry. The wire holds an orientation rather than a free ratio, so a ratio that
// is neither landscape nor portrait is rejected rather than silently widened and
// a caller never believes a framing was applied when it was not.
var omniOrientations = map[string]int{
	"16:9": 1,
	"9:16": 2,
}

type omniOptions struct {
	AspectRatio    string
	NegativePrompt string
}

func (options omniOptions) orientation() int {
	if framing, ok := omniOrientations[options.AspectRatio]; ok {
		return framing
	}
	return webOrientationLandscape
}

func parseOmniOptions(config map[string]json.RawMessage) (omniOptions, error) {
	var options omniOptions
	for key, value := range config {
		switch key {
		case "aspectRatio":
			var ratio string
			if json.Unmarshal(value, &ratio) != nil {
				return omniOptions{}, failure(400, "omni_invalid_aspect_ratio")
			}
			if _, ok := omniOrientations[ratio]; !ok {
				return omniOptions{}, failure(400, "omni_invalid_aspect_ratio")
			}
			options.AspectRatio = ratio
		case "negativePrompt":
			var negative string
			if json.Unmarshal(value, &negative) != nil {
				return omniOptions{}, failure(400, "omni_invalid_negative_prompt")
			}
			options.NegativePrompt = strings.TrimSpace(negative)
		case "candidateCount":
			var count int
			if json.Unmarshal(value, &count) != nil || count != 1 {
				return omniOptions{}, failure(400, "omni_single_candidate_only")
			}
		default:
			// durationSeconds, resolution and personGeneration land here: they
			// have neither a wire slot nor a prompt equivalent that holds.
			return omniOptions{}, failure(400, "omni_unsupported_generation_option")
		}
	}
	return options, nil
}

// Unlike the framing, a negative prompt has no slot of its own, so it stays part
// of the prompt text.
func (options omniOptions) applyPrompt(prompt string) string {
	if options.NegativePrompt == "" {
		return prompt
	}
	return prompt + "\n\nDo not include: " + options.NegativePrompt
}

func (options omniOptions) generationConfig() map[string]string {
	config := map[string]string{}
	if options.AspectRatio != "" {
		config["aspectRatio"] = options.AspectRatio
	}
	if options.NegativePrompt != "" {
		config["negativePrompt"] = options.NegativePrompt
	}
	return config
}
