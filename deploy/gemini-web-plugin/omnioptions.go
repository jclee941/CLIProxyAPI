package main

import (
	"encoding/json"
	"strings"
)

// omniFraming is how the web app states the framing, and it states it twice:
// the chip the user picked and the orientation that chip translates into. Both
// have to travel and they have to agree - a chip that still says landscape next
// to an orientation that says portrait leaves the upstream holding the stream
// open until the budget runs out.
type omniFraming struct {
	chip        int
	orientation int
}

// webFramingLandscape is what the web app sends when its own control is left
// alone, and what this payload carried before the surface chose a default.
var webFramingLandscape = omniFraming{chip: 16, orientation: 1}

// webFramingPortrait is the pair the app sends for the portrait chip.
var webFramingPortrait = omniFraming{chip: 17, orientation: 2}

// omniFramings maps the accepted ratios onto those pairs. The wire holds an
// orientation rather than a free ratio, so a ratio that is neither landscape nor
// portrait is rejected rather than silently widened, and a caller never believes
// a framing was applied when it was not.
var omniFramings = map[string]omniFraming{
	"16:9": webFramingLandscape,
	"9:16": webFramingPortrait,
}

type omniOptions struct {
	AspectRatio    string
	NegativePrompt string
	// ThinkingLevel is the web app's thinking level name for the turn; empty sends
	// the standard level.
	ThinkingLevel string
	// Language is the interface language a video turn is sent with, one of
	// omniLanguages; empty sends webLanguageDefault.
	Language string
	// VideoMode is the capability mode a video turn runs on; zero takes the
	// account's standard mode, and a mode the account does not advertise is
	// refused.
	VideoMode int
}

// webLanguageDefault is the interface language every request has always been
// sent with. A video turn sends another only when the caller names it.
const webLanguageDefault = "en"

// omniLanguages are the interface languages a video turn may be sent with.
var omniLanguages = map[string]bool{"en": true, "ko": true}

// language is the interface language the turn is sent with.
func (options omniOptions) language() string {
	if options.Language != "" {
		return options.Language
	}
	return webLanguageDefault
}

// omniThinkingLevels are the web app's thinking levels by name, as its StreamGenerate
// builder numbers them in slot 80.
var omniThinkingLevels = map[string]int{"STANDARD": 1, "EXTENDED": 2, "DEEP_THINK": 3, "MEDIUM": 4}

// thinking is the slot 80 value the turn is sent with.
func (options omniOptions) thinking() int {
	if level, ok := omniThinkingLevels[options.ThinkingLevel]; ok {
		return level
	}
	return webVideoThinkingLevel
}

// framing answers what the turn is shot as. A caller who named a ratio gets it;
// anything else is framed vertically, which is the default this surface serves.
func (options omniOptions) framing() omniFraming {
	if framing, ok := omniFramings[options.AspectRatio]; ok {
		return framing
	}
	return webFramingPortrait
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
			if _, ok := omniFramings[ratio]; !ok {
				return omniOptions{}, failure(400, "omni_invalid_aspect_ratio")
			}
			options.AspectRatio = ratio
		case "negativePrompt":
			var negative string
			if json.Unmarshal(value, &negative) != nil {
				return omniOptions{}, failure(400, "omni_invalid_negative_prompt")
			}
			options.NegativePrompt = strings.TrimSpace(negative)
		case "thinkingLevel":
			var level string
			if json.Unmarshal(value, &level) != nil {
				return omniOptions{}, failure(400, "omni_invalid_thinking_level")
			}
			level = strings.ToUpper(strings.TrimSpace(level))
			if _, ok := omniThinkingLevels[level]; !ok {
				return omniOptions{}, failure(400, "omni_invalid_thinking_level")
			}
			options.ThinkingLevel = level
		case "language":
			var language string
			if json.Unmarshal(value, &language) != nil || !omniLanguages[language] {
				return omniOptions{}, failure(400, "omni_invalid_language")
			}
			options.Language = language
		case "videoMode":
			var mode int
			if json.Unmarshal(value, &mode) != nil || mode < 1 {
				return omniOptions{}, failure(400, "omni_invalid_video_mode")
			}
			options.VideoMode = mode
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
	if options.ThinkingLevel != "" {
		config["thinkingLevel"] = options.ThinkingLevel
	}
	return config
}
