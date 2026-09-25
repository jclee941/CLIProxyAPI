package main

import "encoding/json"

const previousVideoDeclaration = "[# Sources <PREVIOUS_VIDEO>@Video1] "

// withPreviousVideoDeclaration names the previous turn's video as this turn's
// source. The prompt is the first part every interaction builds, and a caller
// who wrote their own role means it and is left alone.
func withPreviousVideoDeclaration(payload []byte) ([]byte, error) {
	var content map[string]any
	if json.Unmarshal(payload, &content) != nil {
		return nil, failure(500, "chained_interaction_unreadable")
	}
	contents, ok := content["contents"].([]any)
	if !ok || len(contents) != 1 {
		return nil, failure(500, "chained_interaction_unreadable")
	}
	turn, ok := contents[0].(map[string]any)
	if !ok {
		return nil, failure(500, "chained_interaction_unreadable")
	}
	parts, ok := turn["parts"].([]any)
	if !ok || len(parts) == 0 {
		return nil, failure(500, "chained_interaction_unreadable")
	}
	first, ok := parts[0].(map[string]any)
	if !ok {
		return nil, failure(500, "chained_interaction_unreadable")
	}
	prompt, written := first["text"].(string)
	if !written {
		return nil, failure(500, "chained_interaction_unreadable")
	}
	if webRoleDeclared(prompt) {
		return payload, nil
	}
	first["text"] = previousVideoDeclaration + prompt
	return json.Marshal(content)
}
