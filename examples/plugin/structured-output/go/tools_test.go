package main

import (
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/tidwall/gjson"
	"gopkg.in/yaml.v3"
)

const weatherTools = `"tools":[{"type":"function","function":{"name":"get_weather","description":"Look up weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}}}]`

const geminiWeatherTools = `"tools":[{"functionDeclarations":[{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}}]}]`

func TestParseToolsReadsRequirementInBothDialects(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		demands bool
		forced  string
	}{
		{"openai required", `{"messages":[],` + weatherTools + `,"tool_choice":"required"}`, true, ""},
		{"openai forced", `{"messages":[],` + weatherTools + `,"tool_choice":{"type":"function","function":{"name":"get_weather"}}}`, true, "get_weather"},
		{"openai auto", `{"messages":[],` + weatherTools + `,"tool_choice":"auto"}`, false, ""},
		{"openai unspecified", `{"messages":[],` + weatherTools + `}`, false, ""},
		{"gemini any", `{"contents":[],` + geminiWeatherTools + `,"toolConfig":{"functionCallingConfig":{"mode":"ANY"}}}`, true, ""},
		{"gemini auto", `{"contents":[],` + geminiWeatherTools + `,"toolConfig":{"functionCallingConfig":{"mode":"AUTO"}}}`, false, ""},
		{"no tools", `{"messages":[]}`, false, ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			spec := parseTools([]byte(testCase.payload))
			if spec.demandsCall() != testCase.demands {
				t.Fatalf("demandsCall = %v, want %v", spec.demandsCall(), testCase.demands)
			}
			if testCase.forced != "" && spec.Forced != testCase.forced {
				t.Fatalf("forced = %q, want %q", spec.Forced, testCase.forced)
			}
		})
	}
}

func TestParseToolEnvelopeAcceptsCommonShapes(t *testing.T) {
	cases := []struct {
		name      string
		candidate string
		arguments string
	}{
		{"wrapped", `{"tool_calls":[{"name":"get_weather","arguments":{"city":"Seoul"}}]}`, `{"city":"Seoul"}`},
		{"bare call", `{"name":"get_weather","arguments":{"city":"Seoul"}}`, `{"city":"Seoul"}`},
		{"array", `[{"name":"get_weather","arguments":{"city":"Seoul"}}]`, `{"city":"Seoul"}`},
		{"stringified arguments", `{"name":"get_weather","arguments":"{\"city\":\"Seoul\"}"}`, `{"city":"Seoul"}`},
		{"gemini args key", `{"name":"get_weather","args":{"city":"Seoul"}}`, `{"city":"Seoul"}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			calls, ok := parseToolEnvelope(testCase.candidate)
			if !ok || len(calls) != 1 {
				t.Fatalf("envelope not parsed: %v %v", calls, ok)
			}
			if calls[0].Name != "get_weather" || calls[0].Arguments != testCase.arguments {
				t.Fatalf("call = %+v", calls[0])
			}
		})
	}
	if _, ok := parseToolEnvelope(`{"answer":"it is sunny"}`); ok {
		t.Fatal("a plain answer must not parse as a tool call")
	}
}

func TestToolViolationsRejectUnknownFunctionAndBadArguments(t *testing.T) {
	spec := parseTools([]byte(`{"messages":[],` + weatherTools + `,"tool_choice":"required"}`))
	cases := []struct {
		name      string
		candidate string
		extracted bool
		want      string
	}{
		{"no call at all", "", false, "the reply carries no function call"},
		{"unknown function", `{"tool_calls":[{"name":"launch_rocket","arguments":{}}]}`, true, `tool_calls[0].name "launch_rocket" is not an available function; choose one of get_weather`},
		{"missing argument", `{"tool_calls":[{"name":"get_weather","arguments":{}}]}`, true, "tool_calls[0].arguments: city is required but missing"},
		{"wrong argument type", `{"tool_calls":[{"name":"get_weather","arguments":{"city":7}}]}`, true, "tool_calls[0].arguments: city must be string but is integer"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			violations := toolViolations(spec, testCase.candidate, testCase.extracted)
			if !containsString(violations, testCase.want) {
				t.Fatalf("violations %v do not contain %q", violations, testCase.want)
			}
		})
	}
	if violations := toolViolations(spec, `{"tool_calls":[{"name":"get_weather","arguments":{"city":"Seoul"}}]}`, true); len(violations) != 0 {
		t.Fatalf("a valid call reported violations: %v", violations)
	}
}

func TestEnforceResponseEmitsNativeOpenAIToolCall(t *testing.T) {
	request := []byte(`{"messages":[{"role":"user","content":"weather?"}],` + weatherTools + `,"tool_choice":"required"}`)
	req := pluginapi.ResponseInterceptRequest{
		SourceFormat:    "openai",
		Model:           "gemini-web-flash-3.8",
		OriginalRequest: request,
		Body:            openAIBody(t, "```json\n{\"tool_calls\":[{\"name\":\"get_weather\",\"arguments\":{\"city\":\"Seoul\"}}]}\n```"),
	}
	body, changed := enforceResponse(req, testConfig(0))
	if !changed {
		t.Fatal("a prose tool call was not converted")
	}
	if name := gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String(); name != "get_weather" {
		t.Fatalf("tool call = %s", body)
	}
	if arguments := gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments"); arguments.Type != gjson.String || arguments.String() != `{"city":"Seoul"}` {
		t.Fatalf("arguments must be a JSON string, got %s", arguments.Raw)
	}
	if finish := gjson.GetBytes(body, "choices.0.finish_reason").String(); finish != "tool_calls" {
		t.Fatalf("finish_reason = %q", finish)
	}
	if content := gjson.GetBytes(body, "choices.0.message.content"); content.Type != gjson.Null {
		t.Fatalf("content = %s, want null", content.Raw)
	}
}

func TestEnforceResponseEmitsNativeGeminiFunctionCall(t *testing.T) {
	request := []byte(`{"contents":[{"role":"user","parts":[{"text":"weather?"}]}],` + geminiWeatherTools + `,"toolConfig":{"functionCallingConfig":{"mode":"ANY"}}}`)
	req := pluginapi.ResponseInterceptRequest{
		SourceFormat:    "gemini",
		Model:           "gemini-web-flash-3.8",
		OriginalRequest: request,
		Body:            geminiBody(t, `{"tool_calls":[{"name":"get_weather","arguments":{"city":"Seoul"}}]}`),
	}
	body, changed := enforceResponse(req, testConfig(0))
	if !changed {
		t.Fatal("a prose tool call was not converted")
	}
	if name := gjson.GetBytes(body, "candidates.0.content.parts.0.functionCall.name").String(); name != "get_weather" {
		t.Fatalf("function call = %s", body)
	}
	if city := gjson.GetBytes(body, "candidates.0.content.parts.0.functionCall.args.city").String(); city != "Seoul" {
		t.Fatalf("args = %s", body)
	}
}

func TestEnforceResponseLeavesNativeToolCallsUntouched(t *testing.T) {
	request := []byte(`{"messages":[{"role":"user","content":"weather?"}],` + weatherTools + `,"tool_choice":"required"}`)
	native := []byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_upstream","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Seoul\"}"}}]},"finish_reason":"tool_calls"}]}`)
	req := pluginapi.ResponseInterceptRequest{
		SourceFormat:    "openai",
		Model:           "gpt-5.5",
		OriginalRequest: request,
		Body:            native,
	}
	if body, changed := enforceResponse(req, testConfig(2)); changed {
		t.Fatalf("a native tool call must not be rewritten: %s", body)
	}
	if !hasNativeToolCall(native) {
		t.Fatal("hasNativeToolCall missed an OpenAI tool call")
	}
}

func TestWithContractInstructsOnlyTheScopedModels(t *testing.T) {
	request := []byte(`{"messages":[{"role":"user","content":"weather?"}],` + weatherTools + `,"tool_choice":"required"}`)
	scoped := defaultConfig()
	scoped.InstructTools = toolInstructionScope{Patterns: []string{"gemini-web"}}

	instructed := withContract(request, scoped, "gemini-web-flash-3.8")
	if role := gjson.GetBytes(instructed, "messages.0.role").String(); role != "system" {
		t.Fatalf("instruction not prepended for a scoped model: %s", instructed)
	}
	if content := gjson.GetBytes(instructed, "messages.0.content").String(); !strings.Contains(content, "get_weather") {
		t.Fatalf("instruction does not name the function: %q", content)
	}

	for _, model := range []string{"claude-sonnet-4.6", "gpt-5.5", ""} {
		if got := withContract(request, scoped, model); string(got) != string(request) {
			t.Fatalf("model %q is outside the scope but was instructed: %s", model, got)
		}
	}
	if got := withContract(request, defaultConfig(), "gemini-web-flash-3.8"); string(got) != string(request) {
		t.Fatalf("an unset scope must instruct nothing: %s", got)
	}

	all := defaultConfig()
	all.InstructTools = toolInstructionScope{All: true}
	if got := withContract(request, all, "claude-sonnet-4.6"); string(got) == string(request) {
		t.Fatal("instruct_tools: true must still cover every model")
	}
}

// Optional tool use must never be instructed, so a provider that calls functions
// natively keeps doing so for every request that did not demand a call.
func TestWithContractLeavesOptionalToolUseAlone(t *testing.T) {
	scoped := defaultConfig()
	scoped.InstructTools = toolInstructionScope{All: true}
	for _, payload := range []string{
		`{"messages":[{"role":"user","content":"weather?"}],` + weatherTools + `,"tool_choice":"auto"}`,
		`{"messages":[{"role":"user","content":"weather?"}],` + weatherTools + `}`,
		`{"contents":[{"role":"user","parts":[{"text":"weather?"}]}],` + geminiWeatherTools + `,"toolConfig":{"functionCallingConfig":{"mode":"AUTO"}}}`,
	} {
		request := []byte(payload)
		if got := withContract(request, scoped, "gemini-web-flash-3.8"); string(got) != string(request) {
			t.Fatalf("optional tool use was instructed: %s", got)
		}
	}
}

// A deployment already carrying the original boolean must keep parsing, so the
// scope accepts both forms.
func TestToolInstructionScopeAcceptsBoolAndList(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		covers  []string
		ignores []string
	}{
		{"boolean false", "instruct_tools: false", nil, []string{"gemini-web-flash-3.8", "claude-sonnet-4.6"}},
		{"boolean true", "instruct_tools: true", []string{"gemini-web-flash-3.8", "claude-sonnet-4.6"}, nil},
		{"list", "instruct_tools: [gemini-web, chatgpt-web]", []string{"gemini-web-flash-3.8", "CHATGPT-WEB-image"}, []string{"claude-sonnet-4.6", "gpt-5.5"}},
		{"empty list", "instruct_tools: []", nil, []string{"gemini-web-flash-3.8"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := defaultConfig()
			if err := yaml.Unmarshal([]byte(testCase.yaml), &cfg); err != nil {
				t.Fatalf("parse %q: %v", testCase.yaml, err)
			}
			for _, model := range testCase.covers {
				if !cfg.InstructTools.covers(model) {
					t.Fatalf("%q should cover %q", testCase.yaml, model)
				}
			}
			for _, model := range testCase.ignores {
				if cfg.InstructTools.covers(model) {
					t.Fatalf("%q should not cover %q", testCase.yaml, model)
				}
			}
		})
	}
}
