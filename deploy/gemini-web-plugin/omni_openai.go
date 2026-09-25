package main

import (
	"encoding/json"
	"strings"
)

func openAIMessageText(raw json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, true
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return "", false
	}
	var builder strings.Builder
	for _, part := range parts {
		if part.Type != "text" && part.Type != "input_text" {
			return "", false
		}
		builder.WriteString(part.Text)
	}
	return builder.String(), true
}

func openAIPromptForOmni(raw []byte) (string, error) {
	var body struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return "", failure(400, "unsupported_omni_request")
	}
	if body.Stream {
		return "", failure(400, "omni_native_nonstreaming_only")
	}
	if body.Model != "" && body.Model != omniModel {
		return "", failure(400, "unsupported_omni_request")
	}
	if len(body.Messages) != 1 || body.Messages[0].Role != "user" {
		return "", failure(400, "unsupported_omni_request")
	}
	prompt, ok := openAIMessageText(body.Messages[0].Content)
	if !ok || strings.TrimSpace(prompt) == "" {
		return "", failure(400, "unsupported_omni_request")
	}
	return prompt, nil
}

// omniGeminiPayload rebuilds the upstream body from the validated prompt and the
// options the web path honours, so nothing else a caller sent can reach the
// generation call while the framing still survives the rewrite.
func omniGeminiPayload(prompt string, options omniOptions) ([]byte, error) {
	type part struct {
		Text string `json:"text"`
	}
	type turn struct {
		Role  string `json:"role"`
		Parts []part `json:"parts"`
	}
	body := struct {
		Contents         []turn            `json:"contents"`
		GenerationConfig map[string]string `json:"generationConfig,omitempty"`
	}{Contents: []turn{{Role: "user", Parts: []part{{Text: prompt}}}}}
	if config := options.generationConfig(); len(config) != 0 {
		body.GenerationConfig = config
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, failure(500, "unsupported_omni_request")
	}
	if err := validateOmni(payload); err != nil {
		return nil, err
	}
	return payload, nil
}
