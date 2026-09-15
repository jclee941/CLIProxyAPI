package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// toolSpec is a normalized view of the function tools a caller offered and
// whether the caller demanded that one of them be called.
type toolSpec struct {
	Functions []toolFunction
	Required  bool
	Forced    string
}

type toolFunction struct {
	Name        string
	Description string
	Parameters  string
}

// demandsCall reports whether the caller required a function call. Optional tool
// use is left to the provider, because only a demanded call can be judged missing.
func (spec *toolSpec) demandsCall() bool {
	return spec != nil && spec.Required && len(spec.Functions) > 0
}

func (spec *toolSpec) function(name string) (toolFunction, bool) {
	for _, declared := range spec.Functions {
		if declared.Name == name {
			return declared, true
		}
	}
	return toolFunction{}, false
}

func (spec *toolSpec) names() string {
	names := make([]string, 0, len(spec.Functions))
	for _, declared := range spec.Functions {
		names = append(names, declared.Name)
	}
	return strings.Join(names, ", ")
}

func parseTools(payload []byte) *toolSpec {
	if spec := parseOpenAITools(payload); spec != nil {
		return spec
	}
	return parseGeminiTools(payload)
}

func parseOpenAITools(payload []byte) *toolSpec {
	tools := gjson.GetBytes(payload, "tools")
	if !tools.IsArray() {
		return nil
	}
	spec := &toolSpec{}
	tools.ForEach(func(_, tool gjson.Result) bool {
		declaration := tool.Get("function")
		if !declaration.Exists() {
			declaration = tool
		}
		spec.add(declaration)
		return true
	})
	if len(spec.Functions) == 0 {
		return nil
	}
	choice := gjson.GetBytes(payload, "tool_choice")
	switch {
	case choice.Type == gjson.String:
		spec.Required = strings.EqualFold(strings.TrimSpace(choice.String()), "required")
	case choice.IsObject():
		if name := strings.TrimSpace(choice.Get("function.name").String()); name != "" {
			spec.Required, spec.Forced = true, name
		}
	}
	return spec
}

func parseGeminiTools(payload []byte) *toolSpec {
	tools := gjson.GetBytes(payload, "tools")
	if !tools.IsArray() {
		return nil
	}
	spec := &toolSpec{}
	tools.ForEach(func(_, tool gjson.Result) bool {
		tool.Get("functionDeclarations").ForEach(func(_, declaration gjson.Result) bool {
			spec.add(declaration)
			return true
		})
		return true
	})
	if len(spec.Functions) == 0 {
		return nil
	}
	config := gjson.GetBytes(payload, "toolConfig.functionCallingConfig")
	spec.Required = strings.EqualFold(strings.TrimSpace(config.Get("mode").String()), "ANY")
	if allowed := config.Get("allowedFunctionNames"); allowed.IsArray() && len(allowed.Array()) == 1 {
		spec.Forced = allowed.Array()[0].String()
	}
	return spec
}

func (spec *toolSpec) add(declaration gjson.Result) {
	name := strings.TrimSpace(declaration.Get("name").String())
	if name == "" {
		return
	}
	parameters := ""
	if schema := declaration.Get("parameters"); schema.IsObject() {
		parameters = schema.Raw
	}
	spec.Functions = append(spec.Functions, toolFunction{
		Name:        name,
		Description: declaration.Get("description").String(),
		Parameters:  parameters,
	})
}

// hasNativeToolCall reports whether the upstream already produced a real function
// call, in which case there is nothing for this plugin to emulate.
func hasNativeToolCall(body []byte) bool {
	if gjson.GetBytes(body, "choices.0.message.tool_calls").IsArray() {
		return true
	}
	native := false
	gjson.GetBytes(body, "candidates.0.content.parts").ForEach(func(_, part gjson.Result) bool {
		if part.Get("functionCall").IsObject() {
			native = true
			return false
		}
		return true
	})
	return native
}

func toolInstructionText(spec *toolSpec) string {
	lines := []string{
		"You must call one of the available functions.",
		`Reply with a single JSON value and nothing else, in exactly this form: {"tool_calls":[{"name":"<function>","arguments":{...}}]}`,
		"Do not add explanations, prefaces, trailing commentary or Markdown code fences.",
	}
	if spec.Forced != "" {
		lines = append(lines, "Call the function "+spec.Forced+".")
	}
	lines = append(lines, "Available functions:")
	for _, declared := range spec.Functions {
		if spec.Forced != "" && declared.Name != spec.Forced {
			continue
		}
		entry := "- " + declared.Name
		if declared.Description != "" {
			entry += ": " + declared.Description
		}
		lines = append(lines, entry)
		if declared.Parameters != "" {
			lines = append(lines, "  arguments must validate against "+declared.Parameters)
		}
	}
	return strings.Join(lines, "\n")
}

type toolCall struct {
	Name      string
	Arguments string
}

// parseToolEnvelope reads the call envelope the model was asked to produce. A
// bare call and a stringified arguments object are accepted too, because models
// routinely drop the wrapper or quote the arguments.
func parseToolEnvelope(candidate string) ([]toolCall, bool) {
	root := gjson.Parse(candidate)
	var entries []gjson.Result
	switch {
	case root.Get("tool_calls").IsArray():
		entries = root.Get("tool_calls").Array()
	case root.IsArray():
		entries = root.Array()
	case root.IsObject() && (root.Get("name").Exists() || root.Get("function.name").Exists()):
		entries = []gjson.Result{root}
	default:
		return nil, false
	}
	calls := make([]toolCall, 0, len(entries))
	for _, entry := range entries {
		name := firstNonEmpty(entry.Get("name").String(), entry.Get("function.name").String())
		if name == "" {
			return nil, false
		}
		calls = append(calls, toolCall{Name: name, Arguments: toolArguments(entry)})
	}
	if len(calls) == 0 {
		return nil, false
	}
	return calls, true
}

func toolArguments(entry gjson.Result) string {
	for _, key := range []string{"arguments", "args", "function.arguments", "parameters"} {
		value := entry.Get(key)
		if value.IsObject() {
			return value.Raw
		}
		if value.Type == gjson.String {
			if trimmed := strings.TrimSpace(value.String()); json.Valid([]byte(trimmed)) {
				return trimmed
			}
		}
	}
	return "{}"
}

func toolViolations(spec *toolSpec, candidate string, extracted bool) []string {
	if !extracted {
		return []string{"the reply carries no function call"}
	}
	calls, ok := parseToolEnvelope(candidate)
	if !ok {
		return []string{`the reply must be {"tool_calls":[{"name":"<function>","arguments":{...}}]}`}
	}
	var out []string
	for index, call := range calls {
		declared, known := spec.function(call.Name)
		if !known {
			out = append(out, fmt.Sprintf("tool_calls[%d].name %q is not an available function; choose one of %s", index, call.Name, spec.names()))
			continue
		}
		if spec.Forced != "" && call.Name != spec.Forced {
			out = append(out, fmt.Sprintf("tool_calls[%d].name must be %q", index, spec.Forced))
			continue
		}
		if declared.Parameters == "" {
			continue
		}
		for _, violation := range validateValue(&outputSpec{Kind: "json_schema", Schema: declared.Parameters}, call.Arguments) {
			out = append(out, fmt.Sprintf("tool_calls[%d].arguments: %s", index, violation))
		}
	}
	return out
}

// writeToolCalls replaces a prose reply with the native function call shape of
// the dialect, so a caller that demanded a call receives a real one.
func writeToolCalls(body []byte, calls []toolCall) ([]byte, bool) {
	if gjson.GetBytes(body, "choices.0.message").Exists() {
		return writeOpenAIToolCalls(body, calls)
	}
	if gjson.GetBytes(body, "candidates.0.content").Exists() {
		return writeGeminiToolCalls(body, calls)
	}
	return body, false
}

func writeOpenAIToolCalls(body []byte, calls []toolCall) ([]byte, bool) {
	encoded := make([]map[string]any, 0, len(calls))
	for index, call := range calls {
		encoded = append(encoded, map[string]any{
			"id":       toolCallID(index, call),
			"type":     "function",
			"function": map[string]string{"name": call.Name, "arguments": call.Arguments},
		})
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		return body, false
	}
	updated, err := sjson.SetRawBytes(body, "choices.0.message.tool_calls", raw)
	if err != nil {
		return body, false
	}
	updated, err = sjson.SetRawBytes(updated, "choices.0.message.content", []byte("null"))
	if err != nil {
		return body, false
	}
	updated, err = sjson.SetBytes(updated, "choices.0.finish_reason", "tool_calls")
	if err != nil {
		return body, false
	}
	return updated, true
}

func writeGeminiToolCalls(body []byte, calls []toolCall) ([]byte, bool) {
	parts := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		parts = append(parts, map[string]any{
			"functionCall": map[string]any{"name": call.Name, "args": json.RawMessage(call.Arguments)},
		})
	}
	raw, err := json.Marshal(parts)
	if err != nil {
		return body, false
	}
	updated, err := sjson.SetRawBytes(body, "candidates.0.content.parts", raw)
	if err != nil {
		return body, false
	}
	updated, err = sjson.SetBytes(updated, "candidates.0.finishReason", "STOP")
	if err != nil {
		return body, false
	}
	return updated, true
}

// toolCallID derives a stable identifier from the call itself, so a regenerated
// reply does not hand the caller a different id for the same call.
func toolCallID(index int, call toolCall) string {
	digest := fnv.New64a()
	_, _ = digest.Write([]byte(call.Name))
	_, _ = digest.Write([]byte(call.Arguments))
	return fmt.Sprintf("call_%d_%016x", index, digest.Sum64())
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
