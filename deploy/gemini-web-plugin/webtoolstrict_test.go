package main

import (
	"encoding/json"
	"testing"
)

func TestWebCheckToolCallsEnforcesTheDeclaredContract(t *testing.T) {
	const tools = `"tools":[{"functionDeclarations":[{"name":"weather","parameters":{"type":"OBJECT","properties":{"city":{"type":"STRING"},"days":{"type":"integer"},"unit":{"type":"string","enum":["c","f"]}},"required":["city"],"additionalProperties":false}},{"name":"other"}]}]`
	scenarios := []struct {
		name      string
		request   string
		calls     []webFunctionCall
		malformed int
		valid     bool
	}{
		{"valid call", `{` + tools + `}`, []webFunctionCall{{"weather", map[string]any{"city": "Seoul", "days": 3.0, "unit": "c"}}}, 0, true},
		{"text in AUTO", `{` + tools + `}`, nil, 0, true},
		{"undeclared name", `{` + tools + `}`, []webFunctionCall{{"search", map[string]any{}}}, 0, false},
		{"missing required", `{` + tools + `}`, []webFunctionCall{{"weather", map[string]any{"days": 3.0}}}, 0, false},
		{"wrong type", `{` + tools + `}`, []webFunctionCall{{"weather", map[string]any{"city": 5.0}}}, 0, false},
		{"fractional integer", `{` + tools + `}`, []webFunctionCall{{"weather", map[string]any{"city": "x", "days": 1.5}}}, 0, false},
		{"enum miss", `{` + tools + `}`, []webFunctionCall{{"weather", map[string]any{"city": "x", "unit": "k"}}}, 0, false},
		{"extra property", `{` + tools + `}`, []webFunctionCall{{"weather", map[string]any{"city": "x", "zip": "1"}}}, 0, false},
		{"non-object args", `{` + tools + `}`, []webFunctionCall{{"other", "x"}}, 0, false},
		{"malformed block", `{` + tools + `}`, nil, 1, false},
		{"ANY without call", `{` + tools + `,"toolConfig":{"functionCallingConfig":{"mode":"ANY"}}}`, nil, 0, false},
		{"ANY disallowed name", `{` + tools + `,"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["weather"]}}}`, []webFunctionCall{{"other", map[string]any{}}}, 0, false},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			var request map[string]any
			if err := json.Unmarshal([]byte(scenario.request), &request); err != nil {
				t.Fatalf("request: %v", err)
			}
			violation := webCheckToolCalls(request, webToolDefinitions(request), scenario.calls, scenario.malformed)
			if (violation == nil) != scenario.valid {
				t.Fatalf("valid=%t want %t (violation %v)", violation == nil, scenario.valid, violation)
			}
		})
	}
}

// A model that keeps calling an undeclared tool is resent once and then fails
// instead of handing the caller a call it never declared.
func TestNativeTextRejectsAnUndeclaredToolAfterOneRepair(t *testing.T) {
	server := nativeWebServer(t, "```function_call\n{\"name\": \"rm_rf\", \"args\": {}}\n```")
	service, record := nativeService(t, server)
	result := invoke(t, service, "executor.execute", executorRequest{
		AuthID: record.ID, AuthProvider: provider, Model: flashModel,
		Format: "gemini", SourceFormat: "gemini", StorageJSON: jsonFixture(t, record),
		Payload: []byte(`{"contents":[{"role":"user","parts":[{"text":"weather?"}]}],"tools":[{"functionDeclarations":[{"name":"test_tool"}]}]}`),
	})
	if result.OK || result.Error == nil || result.Error.Code != "tool_call_invalid" || result.Error.HTTPStatus != 502 {
		t.Fatalf("undeclared tool reached the caller: %+v", result)
	}
}
