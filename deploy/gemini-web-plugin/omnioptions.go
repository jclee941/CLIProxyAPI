package main

import (
	"encoding/json"
	"strings"
)

// The web video tool carries no structured options on the wire: the app's
// aspect-ratio control is encoded inside the request the app builds, and that
// encoding cannot be reproduced without capturing a real submission, so probing
// for it would burn a generation per attempt. These options are therefore folded
// into the prompt text - what Google's own guidance tells users to do - and are
// best-effort exactly like the official control, not a guarantee.
type omniOptions struct {
	AspectRatio    string
	NegativePrompt string
}

// An unlisted ratio is rejected rather than silently defaulted, so a caller
// never believes a framing was applied when it was dropped.
var omniAspectRatios = map[string]string{
	"16:9": "landscape 16:9",
	"9:16": "vertical 9:16",
	"1:1":  "square 1:1",
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
			if _, ok := omniAspectRatios[ratio]; !ok {
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
			// One submission produces one video; any other count would be a
			// promise the web path cannot keep.
			var count int
			if json.Unmarshal(value, &count) != nil || count != 1 {
				return omniOptions{}, failure(400, "omni_single_candidate_only")
			}
		default:
			// durationSeconds, resolution and personGeneration land here: they
			// have no wire encoding and no prompt equivalent that holds.
			return omniOptions{}, failure(400, "omni_unsupported_generation_option")
		}
	}
	return options, nil
}

// The rebuilt request carries no generationConfig, so re-parsing it yields no
// options and leaves the already-folded prompt unchanged.
func (options omniOptions) apply(prompt string) string {
	directives := make([]string, 0, 2)
	if framing, ok := omniAspectRatios[options.AspectRatio]; ok {
		directives = append(directives, "Frame the video as "+framing+".")
	}
	if options.NegativePrompt != "" {
		directives = append(directives, "Do not include: "+options.NegativePrompt)
	}
	if len(directives) == 0 {
		return prompt
	}
	return prompt + "\n\n" + strings.Join(directives, "\n")
}
