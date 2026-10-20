package main

import (
	"encoding/json"
	"testing"
)

// A caller picks the video turn's thinking level through generation_config; the
// gateway sends the extended level when none is named.
func TestInteractionThinkingLevelReachesTheVideoTurn(t *testing.T) {
	for level, want := range map[string]int{"": webVideoThinkingLevel, "standard": 1, "extended": 2, "EXTENDED": 2} {
		body := `{"model":"` + interactionOmniModel + `","input":"a wave"`
		if level != "" {
			body += `,"generation_config":{"thinking_level":"` + level + `"}`
		}
		body += `}`
		_, payload, err := (tuning{}).parseInteraction([]byte(body))
		if err != nil {
			t.Fatalf("level %q: %v", level, err)
		}
		var content struct {
			GenerationConfig map[string]json.RawMessage `json:"generationConfig"`
		}
		if json.Unmarshal(payload, &content) != nil {
			t.Fatalf("level %q: payload %s", level, payload)
		}
		options, err := parseOmniOptions(content.GenerationConfig)
		if err != nil {
			t.Fatalf("level %q: %v", level, err)
		}
		fields := webVideoFields("a wave", 1, "conversation", options.framing(), options.thinking(0), options.language(), nil)
		if fields[80] != want {
			t.Fatalf("level %q: slot 80 = %#v, want %d", level, fields[80], want)
		}
	}
	body := `{"model":"` + interactionOmniModel + `","input":"a wave","generation_config":{"thinking_level":"loud"}}`
	if _, _, err := (tuning{}).parseInteraction([]byte(body)); err == nil || safeCredentialCode(err) != "omni_invalid_thinking_level" {
		t.Fatalf("unknown level = %v, want omni_invalid_thinking_level", err)
	}
}

func TestConfiguredVideoThinkingLevelAppliesOnlyWhenTheRequestNamesNone(t *testing.T) {
	if got := (omniOptions{}).thinking(3); got != 3 {
		t.Fatalf("configured level: %d, want 3", got)
	}
	if got := (omniOptions{ThinkingLevel: "EXTENDED"}).thinking(3); got != 2 {
		t.Fatalf("requested level: %d, want 2", got)
	}
	if got := (omniOptions{}).thinking(0); got != webVideoThinkingLevel {
		t.Fatalf("default level: %d, want %d", got, webVideoThinkingLevel)
	}
}
