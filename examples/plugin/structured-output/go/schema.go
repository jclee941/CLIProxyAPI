package main

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// maxSchemaDepth bounds schema recursion so a cyclic $ref cannot hang the plugin.
const maxSchemaDepth = 64

// maxViolations bounds how many problems are reported, so a wildly wrong reply
// cannot grow an unbounded correction prompt.
const maxViolations = 20

// validateValue reports every way a decoded reply fails the requested contract.
// An empty result means the reply satisfies it. A schema this plugin cannot read
// yields no violations: a reply is never failed on a contract we cannot judge.
func validateValue(spec *outputSpec, raw string) []string {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return []string{"the reply is not a JSON value"}
	}
	if spec == nil {
		return nil
	}
	if spec.Kind == "json_object" {
		if _, ok := value.(map[string]any); !ok {
			return []string{"the reply must be a JSON object"}
		}
		return nil
	}
	if spec.Schema == "" {
		return nil
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(spec.Schema), &root); err != nil {
		return nil
	}
	validator := &schemaValidator{root: root}
	var out []string
	validator.validate("", root, value, 0, &out)
	return out
}

// schemaValidator checks a decoded value against the JSON Schema subset that
// structured output actually uses: types, required properties, closed objects,
// enums, items and local references.
type schemaValidator struct {
	root map[string]any
}

func (v *schemaValidator) validate(path string, schema map[string]any, value any, depth int, out *[]string) {
	if schema == nil || depth > maxSchemaDepth || len(*out) >= maxViolations {
		return
	}
	if ref, ok := stringField(schema, "$ref"); ok {
		if resolved := v.resolve(ref); resolved != nil {
			v.validate(path, resolved, value, depth+1, out)
		}
		return
	}
	for _, keyword := range []string{"anyOf", "oneOf"} {
		if variants, ok := schemaList(schema, keyword); ok {
			if !v.matchesAny(variants, value, depth) {
				v.report(out, path, "does not match any permitted variant")
			}
			return
		}
	}
	if allowed, ok := schema["enum"].([]any); ok {
		if !containsValue(allowed, value) {
			v.report(out, path, "must be one of "+encodeValue(allowed))
		}
		return
	}
	if expected, ok := schema["const"]; ok {
		if !sameValue(expected, value) {
			v.report(out, path, "must equal "+encodeValue(expected))
		}
		return
	}
	if types := schemaTypes(schema); len(types) > 0 {
		if actual := jsonTypeOf(value); !matchesType(types, actual) {
			v.report(out, path, fmt.Sprintf("must be %s but is %s", strings.Join(types, " or "), actual))
			return
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		v.validateObject(path, schema, typed, depth, out)
	case []any:
		v.validateArray(path, schema, typed, depth, out)
	}
}

func (v *schemaValidator) validateObject(path string, schema map[string]any, value map[string]any, depth int, out *[]string) {
	properties, _ := schema["properties"].(map[string]any)
	for _, name := range requiredNames(schema) {
		if _, present := value[name]; !present {
			v.report(out, child(path, name), "is required but missing")
		}
	}
	closed, declared := schema["additionalProperties"].(bool)
	for _, name := range sortedKeys(value) {
		sub, known := properties[name].(map[string]any)
		if !known {
			if declared && !closed {
				v.report(out, child(path, name), "is not a declared property")
			}
			continue
		}
		v.validate(child(path, name), sub, value[name], depth+1, out)
	}
}

func (v *schemaValidator) validateArray(path string, schema map[string]any, value []any, depth int, out *[]string) {
	if minimum, ok := numberField(schema, "minItems"); ok && float64(len(value)) < minimum {
		v.report(out, path, fmt.Sprintf("must have at least %d items", int(minimum)))
	}
	if maximum, ok := numberField(schema, "maxItems"); ok && float64(len(value)) > maximum {
		v.report(out, path, fmt.Sprintf("must have at most %d items", int(maximum)))
	}
	items, ok := schema["items"].(map[string]any)
	if !ok {
		return
	}
	for index, entry := range value {
		v.validate(fmt.Sprintf("%s[%d]", path, index), items, entry, depth+1, out)
	}
}

func (v *schemaValidator) matchesAny(variants []map[string]any, value any, depth int) bool {
	for _, variant := range variants {
		var scratch []string
		v.validate("", variant, value, depth+1, &scratch)
		if len(scratch) == 0 {
			return true
		}
	}
	return false
}

// resolve follows a local reference into $defs or definitions. Remote references
// are not followed, so an unreachable schema simply goes unchecked.
func (v *schemaValidator) resolve(ref string) map[string]any {
	for _, bucketName := range []string{"$defs", "definitions"} {
		prefix := "#/" + bucketName + "/"
		if !strings.HasPrefix(ref, prefix) {
			continue
		}
		bucket, _ := v.root[bucketName].(map[string]any)
		if sub, ok := bucket[strings.TrimPrefix(ref, prefix)].(map[string]any); ok {
			return sub
		}
	}
	return nil
}

func (v *schemaValidator) report(out *[]string, path, problem string) {
	if len(*out) >= maxViolations {
		return
	}
	location := path
	if location == "" {
		location = "the reply"
	}
	*out = append(*out, location+" "+problem)
}

func child(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// jsonTypeOf names the JSON Schema type of a decoded value. Whole numbers report
// as integer, which also satisfies number.
func jsonTypeOf(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		if !math.IsInf(typed, 0) && !math.IsNaN(typed) && typed == math.Trunc(typed) {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

func matchesType(expected []string, actual string) bool {
	for _, want := range expected {
		if want == actual || want == "number" && actual == "integer" {
			return true
		}
	}
	return false
}

func schemaTypes(schema map[string]any) []string {
	switch typed := schema["type"].(type) {
	case string:
		return []string{typed}
	case []any:
		names := make([]string, 0, len(typed))
		for _, entry := range typed {
			if name, ok := entry.(string); ok {
				names = append(names, name)
			}
		}
		return names
	}
	return nil
}

func schemaList(schema map[string]any, key string) ([]map[string]any, bool) {
	entries, ok := schema[key].([]any)
	if !ok || len(entries) == 0 {
		return nil, false
	}
	variants := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if variant, ok := entry.(map[string]any); ok {
			variants = append(variants, variant)
		}
	}
	return variants, len(variants) > 0
}

func requiredNames(schema map[string]any) []string {
	entries, ok := schema["required"].([]any)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if name, ok := entry.(string); ok {
			names = append(names, name)
		}
	}
	return names
}

func stringField(schema map[string]any, key string) (string, bool) {
	value, ok := schema[key].(string)
	return value, ok && value != ""
}

func numberField(schema map[string]any, key string) (float64, bool) {
	value, ok := schema[key].(float64)
	return value, ok
}

// sortedKeys keeps violation order stable so a correction prompt is reproducible.
func sortedKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func containsValue(allowed []any, value any) bool {
	for _, entry := range allowed {
		if sameValue(entry, value) {
			return true
		}
	}
	return false
}

func sameValue(left, right any) bool {
	return encodeValue(left) == encodeValue(right)
}

func encodeValue(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}
