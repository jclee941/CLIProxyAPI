package main

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func (request webChatRequest) structured() bool {
	switch request.ResponseFormat.Type {
	case "json_object", "json_schema":
		return true
	}
	return false
}

func webChatFormatPrompt(request webChatRequest) string {
	if !request.structured() {
		return ""
	}
	lines := []string{"# Response format", "", "Reply with a single JSON document and nothing else: no markdown, no code fence, no commentary."}
	var schema bytes.Buffer
	if json.Compact(&schema, request.ResponseFormat.JSONSchema.Schema) == nil && schema.Len() > 0 && schema.String() != "null" {
		lines = append(lines, "The document must validate against this JSON Schema: "+schema.String())
	} else if request.ResponseFormat.Type == "json_object" {
		lines = append(lines, "The document must be a JSON object.")
	}
	return strings.Join(lines, "\n")
}

func webOutputSchema(request webChatRequest) (*jsonschema.Schema, error) {
	if !request.structured() {
		return nil, nil
	}
	raw := request.ResponseFormat.JSONSchema.Schema
	if request.ResponseFormat.Type == "json_object" {
		raw = json.RawMessage(`{"type":"object"}`)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, failure(400, "invalid_json_schema")
	}
	if _, ok := document.(map[string]any); !ok {
		return nil, failure(400, "invalid_json_schema")
	}
	compiler := jsonschema.NewCompiler()
	// Only this request's schema and its local definitions may be resolved.
	compiler.UseLoader(jsonschema.SchemeURLLoader{})
	compiler.AssertFormat()
	const resource = "https://cpa.invalid/response.schema.json"
	if err := compiler.AddResource(resource, document); err != nil {
		return nil, failure(400, "invalid_json_schema")
	}
	schema, err := compiler.Compile(resource)
	if err != nil {
		return nil, failure(400, "invalid_json_schema")
	}
	return schema, nil
}

var webCodeFencePattern = regexp.MustCompile("(?s)```(?:json|JSON)?[ \\t]*\\n(.*?)\\n[ \\t]*```")

// webStructuredJSON removes presentation wrappers without changing JSON values.
func webStructuredJSON(text string) (string, error) {
	candidates := []string{strings.TrimSpace(text)}
	for _, match := range webCodeFencePattern.FindAllStringSubmatch(text, -1) {
		candidates = append(candidates, strings.TrimSpace(match[1]))
	}
	if first, last := strings.IndexAny(text, "{["), strings.LastIndexAny(text, "}]"); first >= 0 && last > first {
		candidates = append(candidates, text[first:last+1])
	}
	for _, candidate := range candidates {
		if json.Valid([]byte(candidate)) {
			return candidate, nil
		}
	}
	return "", failure(422, "web_structured_output_invalid")
}

func (reply webReply) structuredJSON(schema *jsonschema.Schema) (string, error) {
	document, err := webStructuredJSON(reply.Text)
	if err != nil {
		if reply.FinishReason == "length" {
			return "", failure(422, "web_structured_output_truncated")
		}
		return "", err
	}
	value, err := jsonschema.UnmarshalJSON(strings.NewReader(document))
	if err != nil || schema.Validate(value) != nil {
		return "", failure(422, "web_structured_output_invalid")
	}
	return document, nil
}
