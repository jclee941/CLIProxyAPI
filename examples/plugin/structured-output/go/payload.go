package main

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// parseSpec reads the output contract from either dialect this plugin enforces:
// an OpenAI chat completion or a Gemini generateContent request.
func parseSpec(payload []byte) *outputSpec {
	if spec := parseResponseFormat(payload); spec != nil {
		return spec
	}
	return parseGenerationConfig(payload)
}

// parseGenerationConfig reads Gemini structured output settings. A schema wins
// over the bare JSON mime type because it carries the actual contract.
func parseGenerationConfig(payload []byte) *outputSpec {
	config := gjson.GetBytes(payload, "generationConfig")
	if !config.Exists() {
		return nil
	}
	for _, key := range []string{"responseJsonSchema", "responseSchema"} {
		if schema := config.Get(key); schema.IsObject() {
			return &outputSpec{Kind: "json_schema", Schema: schema.Raw}
		}
	}
	if strings.EqualFold(strings.TrimSpace(config.Get("responseMimeType").String()), "application/json") {
		return &outputSpec{Kind: "json_object"}
	}
	return nil
}

// requestCarriesContract reports whether the caller asked for something the
// upstream may not honour: a response schema, or a demanded function call.
func requestCarriesContract(payload []byte) bool {
	return parseSpec(payload) != nil || parseTools(payload).demandsCall()
}

// withContract states whichever contract the request carries. A demanded
// function call is stated up front only for the models configured to need it,
// because instructing a provider that calls functions natively would talk it
// out of doing so.
func withContract(payload []byte, cfg pluginConfig, model string) []byte {
	if spec := parseSpec(payload); spec != nil {
		return withInstruction(payload, instructionText(spec))
	}
	if cfg.InstructTools.covers(model) {
		if tools := parseTools(payload); tools.demandsCall() {
			return withInstruction(payload, toolInstructionText(tools))
		}
	}
	return payload
}

// withInstruction states the contract in the dialect the payload is written in.
func withInstruction(payload []byte, text string) []byte {
	if gjson.GetBytes(payload, "messages").IsArray() {
		return withSystemInstruction(payload, text)
	}
	if gjson.GetBytes(payload, "contents").IsArray() {
		return withSystemPart(payload, text)
	}
	return payload
}

// withSystemPart prepends the contract to a Gemini systemInstruction, creating
// one when the caller sent none.
func withSystemPart(payload []byte, text string) []byte {
	instruction, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return payload
	}
	parts := []json.RawMessage{instruction}
	gjson.GetBytes(payload, "systemInstruction.parts").ForEach(func(_, value gjson.Result) bool {
		parts = append(parts, json.RawMessage(value.Raw))
		return true
	})
	encoded, err := json.Marshal(parts)
	if err != nil {
		return payload
	}
	updated, err := sjson.SetRawBytes(payload, "systemInstruction.parts", encoded)
	if err != nil {
		return payload
	}
	return updated
}

// replyTextPath locates the reply text in either dialect, or returns an empty
// string when the response carries none. A structured reply is a single text
// value, so the first text part is the whole reply.
func replyTextPath(body []byte) string {
	if content := gjson.GetBytes(body, "choices.0.message.content"); content.Type == gjson.String {
		return "choices.0.message.content"
	}
	parts := gjson.GetBytes(body, "candidates.0.content.parts")
	if !parts.IsArray() {
		return ""
	}
	for index, part := range parts.Array() {
		if part.Get("text").Type == gjson.String {
			return "candidates.0.content.parts." + strconv.Itoa(index) + ".text"
		}
	}
	return ""
}

// appendCorrection adds the failed reply and the violations it must fix as one
// more conversational turn, in the dialect the request is written in.
func appendCorrection(payload []byte, reply, correction string) ([]byte, bool) {
	if gjson.GetBytes(payload, "messages").IsArray() {
		return appendTurns(payload, "messages", []any{
			map[string]string{"role": "assistant", "content": reply},
			map[string]string{"role": "user", "content": correction},
		})
	}
	if gjson.GetBytes(payload, "contents").IsArray() {
		return appendTurns(payload, "contents", []any{
			geminiTurn("model", reply),
			geminiTurn("user", correction),
		})
	}
	return nil, false
}

func geminiTurn(role, text string) map[string]any {
	return map[string]any{"role": role, "parts": []map[string]string{{"text": text}}}
}

func appendTurns(payload []byte, path string, turns []any) ([]byte, bool) {
	var entries []json.RawMessage
	gjson.GetBytes(payload, path).ForEach(func(_, value gjson.Result) bool {
		entries = append(entries, json.RawMessage(value.Raw))
		return true
	})
	for _, turn := range turns {
		encoded, err := json.Marshal(turn)
		if err != nil {
			return nil, false
		}
		entries = append(entries, encoded)
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return nil, false
	}
	updated, err := sjson.SetRawBytes(payload, path, encoded)
	if err != nil {
		return nil, false
	}
	return updated, true
}
