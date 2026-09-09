package main

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// outputSpec is a normalized response_format request.
type outputSpec struct {
	// Kind is either "json_object" or "json_schema".
	Kind string
	// Schema carries the raw JSON Schema for "json_schema", empty otherwise.
	Schema string
}

// parseResponseFormat reads response_format from a chat completion payload and
// returns nil when the caller did not ask for structured output.
func parseResponseFormat(payload []byte) *outputSpec {
	format := gjson.GetBytes(payload, "response_format")
	if !format.Exists() {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(format.Get("type").String())) {
	case "json_object":
		return &outputSpec{Kind: "json_object"}
	case "json_schema":
		schema := format.Get("json_schema.schema")
		if !schema.Exists() {
			return nil
		}
		return &outputSpec{Kind: "json_schema", Schema: schema.Raw}
	default:
		return nil
	}
}

// instructionText states the output contract to a model that will not have it
// enforced upstream.
func instructionText(spec *outputSpec) string {
	lines := []string{
		"Reply with a single JSON value and nothing else.",
		"Do not add explanations, prefaces, trailing commentary or Markdown code fences.",
	}
	if spec.Kind == "json_schema" && spec.Schema != "" {
		lines = append(lines,
			"The JSON value must validate against this JSON Schema, using exactly the declared properties with no additions:",
			spec.Schema)
	}
	return strings.Join(lines, "\n")
}

// withSystemInstruction prepends a system message carrying the contract.
func withSystemInstruction(payload []byte, text string) []byte {
	instruction, err := json.Marshal(map[string]string{"role": "system", "content": text})
	if err != nil {
		return payload
	}
	messages := []json.RawMessage{instruction}
	gjson.GetBytes(payload, "messages").ForEach(func(_, value gjson.Result) bool {
		messages = append(messages, json.RawMessage(value.Raw))
		return true
	})
	encoded, err := json.Marshal(messages)
	if err != nil {
		return payload
	}
	updated, err := sjson.SetRawBytes(payload, "messages", encoded)
	if err != nil {
		return payload
	}
	return updated
}

// extractJSON recovers the JSON value from a reply that may carry fences or prose.
func extractJSON(text string) (string, bool) {
	candidate := strings.TrimSpace(text)
	if candidate == "" {
		return "", false
	}
	if strings.HasPrefix(candidate, "```") {
		if closing := strings.Index(candidate[3:], "```"); closing >= 0 {
			candidate = candidate[3 : 3+closing]
		} else {
			candidate = strings.TrimLeft(candidate, "`")
		}
		candidate = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(candidate), "json"))
	}
	if json.Valid([]byte(candidate)) {
		return unwrapEncoded(candidate), true
	}
	for _, pair := range [][2]byte{{'{', '}'}, {'[', ']'}} {
		start := strings.IndexByte(candidate, pair[0])
		end := strings.LastIndexByte(candidate, pair[1])
		if start < 0 || end <= start {
			continue
		}
		block := candidate[start : end+1]
		if json.Valid([]byte(block)) {
			return unwrapEncoded(block), true
		}
	}
	return "", false
}

// unwrapEncoded recovers the inner document when a reply arrives double-encoded:
// a JSON string whose own content is an object or array. Models occasionally quote
// their whole answer, and passing that through would hand the caller a string
// where an object was requested.
func unwrapEncoded(candidate string) string {
	var inner string
	if err := json.Unmarshal([]byte(candidate), &inner); err != nil {
		return candidate
	}
	trimmed := strings.TrimSpace(inner)
	if trimmed == "" {
		return candidate
	}
	if (trimmed[0] == '{' || trimmed[0] == '[') && json.Valid([]byte(trimmed)) {
		return trimmed
	}
	return candidate
}
