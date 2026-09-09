// Package structuredoutput enforces OpenAI `response_format` for upstreams that
// ignore it.
//
// Some providers are bridges onto chat products rather than the platform API, so
// they accept `response_format` (or the Gemini `responseJsonSchema` translation of
// it) and then answer with prose anyway. For those providers the contract is held
// here instead: the schema is stated to the model, the reply is stripped down to
// its JSON value, and the value is validated. The caller retries with the recorded
// errors until the reply conforms or the attempt budget runs out.
//
// Only the subset OpenAI's strict mode permits is validated: type, properties,
// required, additionalProperties, items, enum, anyOf and $defs/$ref.
package structuredoutput

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MaxAttempts bounds how many times a reply may be regenerated before the request
// fails. One generation plus two corrections keeps latency bounded while still
// giving the model a chance to fix an ordinary mistake.
const MaxAttempts = 3

// Spec is a normalized `response_format` request.
type Spec struct {
	// Kind is either "json_object" or "json_schema".
	Kind string
	// Schema is the decoded JSON Schema; nil for "json_object".
	Schema map[string]any
	// Name identifies the schema in error messages.
	Name string
	// Strict mirrors the client's strict flag.
	Strict bool
	// Defs holds $defs entries referenced by $ref.
	Defs map[string]any
}

// Enforced reports whether a schema must be satisfied, as opposed to only
// requiring that the reply be valid JSON.
func (s *Spec) Enforced() bool {
	return s != nil && s.Kind == "json_schema" && s.Schema != nil
}

// Parse reads `response_format` from a chat completion payload. It returns nil
// when the client did not ask for structured output.
func Parse(payload []byte) *Spec {
	var body struct {
		ResponseFormat *struct {
			Type       string `json:"type"`
			JSONSchema *struct {
				Name   string         `json:"name"`
				Strict bool           `json:"strict"`
				Schema map[string]any `json:"schema"`
			} `json:"json_schema"`
		} `json:"response_format"`
	}
	if err := json.Unmarshal(payload, &body); err != nil || body.ResponseFormat == nil {
		return nil
	}

	switch strings.TrimSpace(strings.ToLower(body.ResponseFormat.Type)) {
	case "json_object":
		return &Spec{Kind: "json_object"}
	case "json_schema":
		descriptor := body.ResponseFormat.JSONSchema
		if descriptor == nil || descriptor.Schema == nil {
			return nil
		}
		name := descriptor.Name
		if name == "" {
			name = "response"
		}
		defs, _ := descriptor.Schema["$defs"].(map[string]any)
		return &Spec{
			Kind:   "json_schema",
			Schema: descriptor.Schema,
			Name:   name,
			Strict: descriptor.Strict,
			Defs:   defs,
		}
	default:
		return nil
	}
}

// InstructionText is the system message that states the output contract.
func InstructionText(spec *Spec) string {
	lines := []string{
		"Reply with a single JSON value and nothing else.",
		"Do not add explanations, prefaces, trailing commentary or Markdown code fences.",
	}
	if spec.Enforced() {
		if encoded, err := json.Marshal(spec.Schema); err == nil {
			lines = append(lines,
				"The JSON value must validate against this JSON Schema, using exactly the declared properties with no additions:",
				string(encoded))
		}
	}
	return strings.Join(lines, "\n")
}

// ExtractJSON recovers the JSON value from a reply that may carry fences or prose.
func ExtractJSON(text string) (string, bool) {
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
		return candidate, true
	}
	for _, pair := range [][2]byte{{'{', '}'}, {'[', ']'}} {
		start := strings.IndexByte(candidate, pair[0])
		end := strings.LastIndexByte(candidate, pair[1])
		if start < 0 || end <= start {
			continue
		}
		block := candidate[start : end+1]
		if json.Valid([]byte(block)) {
			return block, true
		}
	}
	return "", false
}

// Coerce returns the cleaned JSON text, or the reasons the reply is unusable.
func Coerce(text string, spec *Spec) (string, []string) {
	candidate, ok := ExtractJSON(text)
	if !ok {
		return "", []string{"the reply does not contain a JSON value"}
	}
	var value any
	if err := json.Unmarshal([]byte(candidate), &value); err != nil {
		return "", []string{"the reply does not contain a JSON value"}
	}
	if !spec.Enforced() {
		return candidate, nil
	}
	if problems := Validate(value, spec.Schema, spec.Defs, "$"); len(problems) > 0 {
		return "", problems
	}
	return candidate, nil
}

// CorrectionText is the follow-up instruction sent after a non-conforming reply.
func CorrectionText(problems []string) string {
	if len(problems) > 10 {
		problems = problems[:10]
	}
	var builder strings.Builder
	builder.WriteString("The previous reply did not satisfy the required output contract:\n")
	for _, problem := range problems {
		builder.WriteString("- ")
		builder.WriteString(problem)
		builder.WriteString("\n")
	}
	builder.WriteString("Send the corrected JSON value alone, with no other text.")
	return builder.String()
}

// Validate collects schema violations for the strict-mode subset.
func Validate(value any, schema map[string]any, defs map[string]any, path string) []string {
	schema = resolve(schema, defs)

	if options, ok := schema["anyOf"].([]any); ok && len(options) > 0 {
		for _, option := range options {
			branch, ok := option.(map[string]any)
			if ok && len(Validate(value, branch, defs, path)) == 0 {
				return nil
			}
		}
		return []string{fmt.Sprintf("%s: does not match any anyOf branch", path)}
	}

	if allowed, ok := schema["enum"].([]any); ok {
		if !containsValue(allowed, value) {
			return []string{fmt.Sprintf("%s: %v is not one of the allowed values", path, value)}
		}
	}

	if expected := declaredTypes(schema); len(expected) > 0 && !matchesAnyType(value, expected) {
		return []string{fmt.Sprintf("%s: expected type %s", path, strings.Join(expected, "|"))}
	}

	var problems []string
	switch typed := value.(type) {
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		for _, name := range stringList(schema["required"]) {
			if _, present := typed[name]; !present {
				problems = append(problems, fmt.Sprintf("%s: missing required property %q", path, name))
			}
		}
		if additional, ok := schema["additionalProperties"].(bool); ok && !additional {
			for name := range typed {
				if _, declared := properties[name]; !declared {
					problems = append(problems, fmt.Sprintf("%s: property %q is not allowed", path, name))
				}
			}
		}
		for name, rawChild := range properties {
			child, ok := rawChild.(map[string]any)
			if !ok {
				continue
			}
			if item, present := typed[name]; present {
				problems = append(problems, Validate(item, child, defs, path+"."+name)...)
			}
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for index, item := range typed {
				problems = append(problems, Validate(item, items, defs, fmt.Sprintf("%s[%d]", path, index))...)
			}
		}
	}
	return problems
}

func resolve(schema map[string]any, defs map[string]any) map[string]any {
	reference, ok := schema["$ref"].(string)
	if !ok {
		return schema
	}
	name := reference[strings.LastIndexByte(reference, '/')+1:]
	if target, ok := defs[name].(map[string]any); ok {
		return target
	}
	return schema
}

func declaredTypes(schema map[string]any) []string {
	switch declared := schema["type"].(type) {
	case string:
		return []string{declared}
	case []any:
		return stringList(declared)
	default:
		return nil
	}
}

func matchesAnyType(value any, expected []string) bool {
	for _, name := range expected {
		if matchesType(value, name) {
			return true
		}
	}
	return false
}

// matchesType applies JSON Schema semantics: booleans are not numbers, and a
// float is an integer only when it has no fractional part, which is how JSON
// decoding represents whole numbers.
func matchesType(value any, name string) bool {
	switch name {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && number == float64(int64(number))
	default:
		return true
	}
}

func containsValue(allowed []any, value any) bool {
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	for _, candidate := range allowed {
		if other, err := json.Marshal(candidate); err == nil && string(other) == string(encoded) {
			return true
		}
	}
	return false
}

func stringList(raw any) []string {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			values = append(values, text)
		}
	}
	return values
}
