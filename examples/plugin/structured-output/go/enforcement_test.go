package main

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/tidwall/gjson"
)

func TestParseSpecReadsBothDialects(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		kind    string
	}{
		{"openai schema", `{"messages":[],"response_format":{"type":"json_schema","json_schema":{"schema":{"type":"object"}}}}`, "json_schema"},
		{"openai object", `{"messages":[],"response_format":{"type":"json_object"}}`, "json_object"},
		{"gemini json schema", `{"contents":[],"generationConfig":{"responseMimeType":"application/json","responseJsonSchema":{"type":"object"}}}`, "json_schema"},
		{"gemini response schema", `{"contents":[],"generationConfig":{"responseSchema":{"type":"object"}}}`, "json_schema"},
		{"gemini mime only", `{"contents":[],"generationConfig":{"responseMimeType":"application/json"}}`, "json_object"},
		{"unstructured", `{"messages":[],"generationConfig":{"temperature":1}}`, ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			spec := parseSpec([]byte(testCase.payload))
			if testCase.kind == "" {
				if spec != nil {
					t.Fatalf("expected no contract, got %+v", spec)
				}
				return
			}
			if spec == nil || spec.Kind != testCase.kind {
				t.Fatalf("expected kind %q, got %+v", testCase.kind, spec)
			}
		})
	}
}

func TestReplyTextPathLocatesReplyInBothDialects(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"openai", `{"choices":[{"message":{"content":"hi"}}]}`, "choices.0.message.content"},
		{"gemini past a non-text part", `{"candidates":[{"content":{"parts":[{"inlineData":{}},{"text":"hi"}]}}]}`, "candidates.0.content.parts.1.text"},
		{"no text part", `{"candidates":[{"content":{"parts":[{"inlineData":{}}]}}]}`, ""},
		{"openai content parts", `{"choices":[{"message":{"content":[{"type":"text","text":"hi"}]}}]}`, ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := replyTextPath([]byte(testCase.body)); got != testCase.want {
				t.Fatalf("replyTextPath = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestWithInstructionStatesContractInEachDialect(t *testing.T) {
	openai := withInstruction([]byte(`{"messages":[{"role":"user","content":"hi"}]}`), "CONTRACT")
	if role := gjson.GetBytes(openai, "messages.0.role").String(); role != "system" {
		t.Fatalf("first message role = %q, want system", role)
	}
	if content := gjson.GetBytes(openai, "messages.0.content").String(); content != "CONTRACT" {
		t.Fatalf("instruction = %q", content)
	}
	if kept := gjson.GetBytes(openai, "messages.1.content").String(); kept != "hi" {
		t.Fatalf("original message lost, got %q", kept)
	}

	gemini := withInstruction([]byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`), "CONTRACT")
	if instruction := gjson.GetBytes(gemini, "systemInstruction.parts.0.text").String(); instruction != "CONTRACT" {
		t.Fatalf("gemini instruction = %q", instruction)
	}
	if kept := gjson.GetBytes(gemini, "contents.0.parts.0.text").String(); kept != "hi" {
		t.Fatalf("original turn lost, got %q", kept)
	}

	untouched := []byte(`{"prompt":"hi"}`)
	if got := withInstruction(untouched, "CONTRACT"); string(got) != string(untouched) {
		t.Fatalf("unknown dialect rewritten: %s", got)
	}
}

func TestAppendCorrectionAddsTurnsInEachDialect(t *testing.T) {
	openai, ok := appendCorrection([]byte(`{"messages":[{"role":"user","content":"hi"}]}`), "BAD", "FIX")
	if !ok {
		t.Fatal("openai correction refused")
	}
	if role := gjson.GetBytes(openai, "messages.1.role").String(); role != "assistant" {
		t.Fatalf("failed reply role = %q, want assistant", role)
	}
	if reply := gjson.GetBytes(openai, "messages.1.content").String(); reply != "BAD" {
		t.Fatalf("failed reply = %q", reply)
	}
	if correction := gjson.GetBytes(openai, "messages.2.content").String(); correction != "FIX" {
		t.Fatalf("correction = %q", correction)
	}

	gemini, ok := appendCorrection([]byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`), "BAD", "FIX")
	if !ok {
		t.Fatal("gemini correction refused")
	}
	if role := gjson.GetBytes(gemini, "contents.1.role").String(); role != "model" {
		t.Fatalf("failed reply role = %q, want model", role)
	}
	if reply := gjson.GetBytes(gemini, "contents.1.parts.0.text").String(); reply != "BAD" {
		t.Fatalf("failed reply = %q", reply)
	}
	if role := gjson.GetBytes(gemini, "contents.2.role").String(); role != "user" {
		t.Fatalf("correction role = %q, want user", role)
	}

	if _, ok := appendCorrection([]byte(`{"prompt":"hi"}`), "BAD", "FIX"); ok {
		t.Fatal("unknown dialect must refuse a correction")
	}
}

func TestNonStreamingClearsStreamingFlag(t *testing.T) {
	if streaming := gjson.GetBytes(nonStreaming([]byte(`{"stream":true,"messages":[]}`)), "stream").Bool(); streaming {
		t.Fatal("replayed request still asks for streaming")
	}
	payload := []byte(`{"messages":[]}`)
	if got := nonStreaming(payload); string(got) != string(payload) {
		t.Fatalf("request without a streaming flag rewritten: %s", got)
	}
}

func TestEnforceReplyReducesFencedReplyToItsJSONValue(t *testing.T) {
	request := []byte(`{"messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_object"}}`)
	req := pluginapi.ResponseInterceptRequest{
		SourceFormat:    "openai",
		Model:           "gemini-web-flash-3.8",
		OriginalRequest: request,
		Body:            openAIBody(t, "Sure, here you go:\n```json\n{\"a\":1}\n```"),
	}
	body, changed := enforceResponse(req, testConfig(0))
	if !changed {
		t.Fatal("fenced reply was not rewritten")
	}
	if got := gjson.GetBytes(body, "choices.0.message.content").String(); got != `{"a":1}` {
		t.Fatalf("cleaned reply = %q", got)
	}
}

func TestEnforceReplyReducesGeminiReply(t *testing.T) {
	request := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"responseMimeType":"application/json"}}`)
	req := pluginapi.ResponseInterceptRequest{
		SourceFormat:    "gemini",
		Model:           "gemini-web-flash-3.8",
		OriginalRequest: request,
		Body:            geminiBody(t, "```json\n{\"a\":1}\n```"),
	}
	body, changed := enforceResponse(req, testConfig(0))
	if !changed {
		t.Fatal("gemini reply was not rewritten")
	}
	if got := gjson.GetBytes(body, "candidates.0.content.parts.0.text").String(); got != `{"a":1}` {
		t.Fatalf("cleaned reply = %q", got)
	}
}

func TestEnforceReplyLeavesConformingReplyUntouched(t *testing.T) {
	request := []byte(`{"messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_object"}}`)
	req := pluginapi.ResponseInterceptRequest{
		SourceFormat:    "openai",
		Model:           "gpt-5.5",
		OriginalRequest: request,
		Body:            openAIBody(t, `{"a":1}`),
	}
	if body, changed := enforceResponse(req, testConfig(2)); changed {
		t.Fatalf("a conforming reply must not be rewritten: %s", body)
	}
}

// A violating reply must still reach the caller when regeneration cannot run, so
// enforcement degrades to cleaning rather than failing the request.
func TestEnforceReplyFallsBackWhenRegenerationIsUnavailable(t *testing.T) {
	request := []byte(`{"messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_schema","json_schema":{"schema":{"type":"object","properties":{"a":{"type":"integer"}},"required":["a"],"additionalProperties":false}}}}`)
	req := pluginapi.ResponseInterceptRequest{
		SourceFormat:    "openai",
		Model:           "gemini-web-flash-3.8",
		OriginalRequest: request,
		Body:            openAIBody(t, "```json\n{\"b\":2}\n```"),
	}
	body, changed := enforceResponse(req, testConfig(2))
	if !changed {
		t.Fatal("fenced reply was not cleaned")
	}
	if got := gjson.GetBytes(body, "choices.0.message.content").String(); got != `{"b":2}` {
		t.Fatalf("cleaned reply = %q", got)
	}
}

func testConfig(maxAttempts int) pluginConfig {
	cfg := defaultConfig()
	cfg.MaxAttempts = maxAttempts
	return cfg
}

func openAIBody(t *testing.T, content string) []byte {
	t.Helper()
	return []byte(`{"choices":[{"message":{"role":"assistant","content":` + encodeString(t, content) + `}}]}`)
}

func geminiBody(t *testing.T, text string) []byte {
	t.Helper()
	return []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":` + encodeString(t, text) + `}]}}]}`)
}

func encodeString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode %q: %v", value, err)
	}
	return string(encoded)
}
