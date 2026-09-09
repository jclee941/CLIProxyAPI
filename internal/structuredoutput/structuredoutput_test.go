package structuredoutput

import (
	"encoding/json"
	"strings"
	"testing"
)

func decode(t *testing.T, raw string) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return value
}

const citySchema = `{"type":"object","properties":{"city":{"type":"string"},"population":{"type":"integer"}},"required":["city","population"],"additionalProperties":false}`

func citySpec(t *testing.T) *Spec {
	t.Helper()
	payload := `{"response_format":{"type":"json_schema","json_schema":{"name":"city_info","strict":true,"schema":` + citySchema + `}}}`
	spec := Parse([]byte(payload))
	if spec == nil || !spec.Enforced() || !spec.Strict {
		t.Fatalf("expected an enforced strict spec, got %+v", spec)
	}
	return spec
}

func TestParseRecognizesSupportedFormats(t *testing.T) {
	if spec := Parse([]byte(`{"response_format":{"type":"json_object"}}`)); spec == nil || spec.Kind != "json_object" || spec.Enforced() {
		t.Fatalf("json_object should parse without a schema, got %+v", spec)
	}
	citySpec(t)
	for _, payload := range []string{`{}`, `{"response_format":{"type":"text"}}`, `{"response_format":{"type":"json_schema"}}`, `not json`} {
		if spec := Parse([]byte(payload)); spec != nil {
			t.Fatalf("payload %s should not produce a spec, got %+v", payload, spec)
		}
	}
}

func TestExtractJSONRecoversValueFromDecoratedReplies(t *testing.T) {
	cases := map[string]string{
		"bare":         `{"a":1}`,
		"fenced":       "```json\n{\"a\":1}\n```",
		"fenced plain": "```\n{\"a\":1}\n```",
		"prose around": `Sure, here it is: {"a":1} hope that helps`,
	}
	for name, reply := range cases {
		got, ok := ExtractJSON(reply)
		if !ok {
			t.Fatalf("%s: expected extraction to succeed", name)
		}
		if !json.Valid([]byte(got)) || !strings.Contains(got, `"a"`) {
			t.Fatalf("%s: unexpected extraction %q", name, got)
		}
	}
	if got, ok := ExtractJSON("prose then [1,2,3] tail"); !ok || got != "[1,2,3]" {
		t.Fatalf("array extraction failed: %q ok=%v", got, ok)
	}
	for _, reply := range []string{"", "no json here"} {
		if _, ok := ExtractJSON(reply); ok {
			t.Fatalf("reply %q should not yield JSON", reply)
		}
	}
}

func TestValidateEnforcesStrictSubset(t *testing.T) {
	schema := decode(t, citySchema)

	if problems := Validate(map[string]any{"city": "Seoul", "population": float64(1)}, schema, nil, "$"); len(problems) != 0 {
		t.Fatalf("conforming value rejected: %v", problems)
	}

	cases := map[string]struct {
		value    map[string]any
		fragment string
	}{
		"extra property":  {map[string]any{"city": "S", "population": float64(1), "x": float64(2)}, "is not allowed"},
		"missing require": {map[string]any{"city": "S"}, "missing required property"},
		"wrong type":      {map[string]any{"city": "S", "population": "many"}, "expected type"},
		"bool not int":    {map[string]any{"city": "S", "population": true}, "expected type"},
	}
	for name, tc := range cases {
		problems := Validate(tc.value, schema, nil, "$")
		if len(problems) == 0 || !strings.Contains(strings.Join(problems, "|"), tc.fragment) {
			t.Fatalf("%s: expected %q, got %v", name, tc.fragment, problems)
		}
	}

	if problems := Validate(float64(3), decode(t, `{"type":"number"}`), nil, "$"); len(problems) != 0 {
		t.Fatalf("3 should be a number: %v", problems)
	}
	if problems := Validate(3.5, decode(t, `{"type":"integer"}`), nil, "$"); len(problems) == 0 {
		t.Fatal("3.5 must not validate as integer")
	}
}

func TestValidateHandlesNestedSchemaFeatures(t *testing.T) {
	nested := decode(t, `{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string"}},"mode":{"enum":["a","b"]},"ref":{"$ref":"#/$defs/Inner"}},"required":["tags"],"additionalProperties":false,"$defs":{"Inner":{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"],"additionalProperties":false}}}`)
	defs, _ := nested["$defs"].(map[string]any)

	ok := map[string]any{"tags": []any{"x"}, "mode": "a", "ref": map[string]any{"n": float64(1)}}
	if problems := Validate(ok, nested, defs, "$"); len(problems) != 0 {
		t.Fatalf("nested conforming value rejected: %v", problems)
	}

	badItem := map[string]any{"tags": []any{"x", float64(2)}}
	if problems := Validate(badItem, nested, defs, "$"); len(problems) == 0 || !strings.Contains(problems[0], "[1]") {
		t.Fatalf("array item index not reported: %v", problems)
	}

	badEnum := map[string]any{"tags": []any{}, "mode": "z"}
	if problems := Validate(badEnum, nested, defs, "$"); len(problems) == 0 || !strings.Contains(problems[0], "allowed values") {
		t.Fatalf("enum violation not reported: %v", problems)
	}

	badRef := map[string]any{"tags": []any{}, "ref": map[string]any{}}
	if problems := Validate(badRef, nested, defs, "$"); len(problems) == 0 || !strings.Contains(strings.Join(problems, "|"), "ref: missing required") {
		t.Fatalf("$ref target not enforced: %v", problems)
	}

	anyOf := decode(t, `{"anyOf":[{"type":"string"},{"type":"integer"}]}`)
	if problems := Validate("s", anyOf, nil, "$"); len(problems) != 0 {
		t.Fatalf("string should match anyOf: %v", problems)
	}
	if problems := Validate(float64(5), anyOf, nil, "$"); len(problems) != 0 {
		t.Fatalf("integer should match anyOf: %v", problems)
	}
	if problems := Validate(1.5, anyOf, nil, "$"); len(problems) == 0 || !strings.Contains(problems[0], "anyOf") {
		t.Fatalf("non-matching value should fail anyOf: %v", problems)
	}
}

func TestCoerceCleansOrExplains(t *testing.T) {
	spec := citySpec(t)

	hostile := "Seoul is the capital.\n\n```json\n{\n  \"city\": \"Seoul\",\n  \"population\": 9602826,\n  \"country\": \"KR\"\n}\n```\n"
	if content, problems := Coerce(hostile, spec); content != "" || len(problems) == 0 || !strings.Contains(problems[0], "is not allowed") {
		t.Fatalf("forbidden property should be reported: content=%q problems=%v", content, problems)
	}

	clean := "```json\n{\"city\":\"Seoul\",\"population\":9602826}\n```"
	content, problems := Coerce(clean, spec)
	if len(problems) != 0 {
		t.Fatalf("conforming reply rejected: %v", problems)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(content), &decoded); err != nil || decoded["city"] != "Seoul" {
		t.Fatalf("unexpected coerced content %q (err=%v)", content, err)
	}

	if _, problems := Coerce("blah blah", spec); len(problems) == 0 || !strings.Contains(problems[0], "does not contain") {
		t.Fatalf("non-JSON reply should be reported: %v", problems)
	}

	loose := Parse([]byte(`{"response_format":{"type":"json_object"}}`))
	if content, problems := Coerce(`{"anything":1}`, loose); len(problems) != 0 || !strings.Contains(content, "anything") {
		t.Fatalf("json_object should accept any object: content=%q problems=%v", content, problems)
	}
}

func TestInstructionTextMentionsSchemaOnlyWhenEnforced(t *testing.T) {
	if text := InstructionText(citySpec(t)); !strings.Contains(text, "JSON Schema") || !strings.Contains(text, "population") {
		t.Fatalf("schema instruction missing the schema: %q", text)
	}
	loose := Parse([]byte(`{"response_format":{"type":"json_object"}}`))
	if text := InstructionText(loose); strings.Contains(text, "JSON Schema") {
		t.Fatalf("json_object instruction should not mention a schema: %q", text)
	}
}

func TestCorrectionTextListsProblems(t *testing.T) {
	text := CorrectionText([]string{"$: missing required property \"city\""})
	if !strings.Contains(text, "missing required property") || !strings.Contains(text, "corrected JSON value alone") {
		t.Fatalf("unexpected correction text: %q", text)
	}
}
