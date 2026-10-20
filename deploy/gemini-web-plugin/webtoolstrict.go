package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// webToolViolation explains why a reply broke the tool contract the caller
// declared. The chat product cannot enforce tools, so the plugin enforces them
// on the reply: an undeclared or disallowed name, arguments outside the declared
// schema, an unreadable call block, or prose where functionCallingConfig ANY
// demanded a call.
type webToolViolation struct{ reason string }

func (violation *webToolViolation) Error() string { return violation.reason }

// webCheckToolCalls validates parsed calls against the request's declarations.
// malformed counts call blocks that were present but could not be decoded.
func webCheckToolCalls(request map[string]any, definitions []webToolDefinition, calls []webFunctionCall, malformed int) *webToolViolation {
	if malformed > 0 {
		return &webToolViolation{fmt.Sprintf("%d function_call block(s) were not valid JSON objects with a name", malformed)}
	}
	declared := make(map[string]webToolDefinition, len(definitions))
	for _, definition := range definitions {
		declared[definition.Name] = definition
	}
	mode := strings.ToUpper(webFunctionCallingMode(request))
	allowed := map[string]bool{}
	if mode == "ANY" {
		config := webMapField(webMapField(request["toolConfig"])["functionCallingConfig"])
		for _, name := range webListField(config["allowedFunctionNames"]) {
			if text, ok := name.(string); ok {
				allowed[text] = true
			}
		}
		if len(calls) == 0 {
			return &webToolViolation{"a tool call was required but the reply contained none"}
		}
	}
	for _, call := range calls {
		definition, ok := declared[call.Name]
		if !ok {
			return &webToolViolation{fmt.Sprintf("tool %q is not declared", call.Name)}
		}
		if len(allowed) > 0 && !allowed[call.Name] {
			return &webToolViolation{fmt.Sprintf("tool %q is not in allowedFunctionNames", call.Name)}
		}
		if _, ok := call.Args.(map[string]any); !ok {
			return &webToolViolation{fmt.Sprintf("tool %q arguments must be a JSON object", call.Name)}
		}
		if definition.Parameters != nil {
			if problem := webSchemaCheck(definition.Parameters, call.Args, "args"); problem != "" {
				return &webToolViolation{fmt.Sprintf("tool %q: %s", call.Name, problem)}
			}
		}
	}
	return nil
}

// webSchemaCheck validates a value against the JSON Schema / OpenAPI subset tool
// declarations use: type (either case), properties, required,
// additionalProperties false, items, enum, nullable and anyOf/oneOf. Keywords
// outside that subset are not enforced.
func webSchemaCheck(schema any, value any, path string) string {
	node, ok := schema.(map[string]any)
	if !ok {
		return ""
	}
	if value == nil && (node["nullable"] == true || webSchemaAllowsType(node, "null")) {
		return ""
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		if options := webListField(node[key]); len(options) > 0 {
			for _, option := range options {
				if webSchemaCheck(option, value, path) == "" {
					return ""
				}
			}
			return path + " matches none of the allowed schemas"
		}
	}
	if enum := webListField(node["enum"]); len(enum) > 0 {
		encoded, _ := json.Marshal(value)
		match := false
		for _, candidate := range enum {
			if other, _ := json.Marshal(candidate); string(other) == string(encoded) {
				match = true
				break
			}
		}
		if !match {
			return path + " is not one of the enum values"
		}
	}
	types := webSchemaTypes(node)
	if len(types) > 0 {
		matched := false
		for _, kind := range types {
			if webValueIsType(value, kind) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Sprintf("%s must be %s", path, strings.Join(types, " or "))
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		properties := webMapField(node["properties"])
		for _, name := range webListField(node["required"]) {
			if text, ok := name.(string); ok {
				if _, present := typed[text]; !present {
					return fmt.Sprintf("%s.%s is required", path, text)
				}
			}
		}
		for key, field := range typed {
			if child, present := properties[key]; present {
				if problem := webSchemaCheck(child, field, path+"."+key); problem != "" {
					return problem
				}
				continue
			}
			switch extra := node["additionalProperties"].(type) {
			case bool:
				if !extra {
					return fmt.Sprintf("%s.%s is not a declared property", path, key)
				}
			case map[string]any:
				if problem := webSchemaCheck(extra, field, path+"."+key); problem != "" {
					return problem
				}
			}
		}
	case []any:
		if items, present := node["items"]; present {
			for index, item := range typed {
				if problem := webSchemaCheck(items, item, fmt.Sprintf("%s[%d]", path, index)); problem != "" {
					return problem
				}
			}
		}
	}
	return ""
}

func webSchemaTypes(node map[string]any) []string {
	switch kind := node["type"].(type) {
	case string:
		return []string{strings.ToLower(kind)}
	case []any:
		types := make([]string, 0, len(kind))
		for _, entry := range kind {
			if text, ok := entry.(string); ok {
				types = append(types, strings.ToLower(text))
			}
		}
		return types
	}
	return nil
}

func webSchemaAllowsType(node map[string]any, kind string) bool {
	for _, entry := range webSchemaTypes(node) {
		if entry == kind {
			return true
		}
	}
	return false
}

func webValueIsType(value any, kind string) bool {
	switch kind {
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
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && number == math.Trunc(number)
	case "null":
		return value == nil
	}
	return true
}

// webToolRepairPrompt is the resend asking the model to restate a reply that
// broke the tool contract.
func webToolRepairPrompt(prompt string, violation *webToolViolation) string {
	return prompt + "\n\nIMPORTANT: Your previous reply was rejected: " + violation.reason +
		". Reply again following the tool contract exactly: use only the declared tools, " +
		"give arguments that match each tool's parameters schema, and emit each call as a ```function_call block."
}
