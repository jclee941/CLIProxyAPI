package helps

import (
	translatorcommon "github.com/router-for-me/CLIProxyAPI/v8/internal/translator/common"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var (
	antigravityGenerationConfigPaths = []string{
		"request.generationConfig", "request.generation_config",
	}
	antigravityResponseSchemaKeys = []string{
		"responseSchema", "responseJsonSchema", "response_schema", "response_json_schema",
	}
)

// ApplyAntigravityClaudeStructuredOutput moves a generation-config response schema into
// the system instruction for Claude models served through Antigravity.
//
// Antigravity implements a response schema for Claude as a forced tool call, which Anthropic
// rejects when thinking is mandatory and which turns thinking off otherwise. The schema keys are
// removed (responseMimeType is left untouched) and the shared Claude structured-output
// instruction is appended to request.systemInstruction. Payloads without a schema are returned
// unchanged. Callers must only use this for Claude models.
func ApplyAntigravityClaudeStructuredOutput(payload []byte) []byte {
	var schemaRaw string
	var schemaPaths []string
	for _, container := range antigravityGenerationConfigPaths {
		for _, key := range antigravityResponseSchemaKeys {
			path := container + "." + key
			schema := gjson.GetBytes(payload, path)
			if !schema.Exists() || schema.Type == gjson.Null {
				continue
			}
			if schemaRaw == "" {
				schemaRaw = schema.Raw
			}
			schemaPaths = append(schemaPaths, path)
		}
	}
	if len(schemaPaths) == 0 {
		return payload
	}

	format, errFormat := sjson.SetRawBytes([]byte(`{"type":"json_schema"}`), "json_schema.schema", []byte(schemaRaw))
	if errFormat != nil {
		return payload
	}
	instruction := translatorcommon.BuildClaudeStructuredOutputInstruction(gjson.ParseBytes(format))
	if instruction == "" {
		return payload
	}
	part, errPart := sjson.SetBytes([]byte(`{}`), "text", instruction)
	if errPart != nil {
		return payload
	}

	out := payload
	for _, path := range schemaPaths {
		if updated, errDelete := sjson.DeleteBytes(out, path); errDelete == nil {
			out = updated
		}
	}

	var (
		updated    []byte
		errAppend  error
		systemRoot = gjson.GetBytes(out, "request.systemInstruction")
	)
	switch {
	case systemRoot.Get("parts").IsArray():
		updated, errAppend = sjson.SetRawBytes(out, "request.systemInstruction.parts.-1", part)
	case systemRoot.IsObject():
		updated, errAppend = sjson.SetRawBytes(out, "request.systemInstruction.parts", append(append([]byte{'['}, part...), ']'))
	default:
		updated, errAppend = sjson.SetRawBytes(out, "request.systemInstruction", append(append([]byte(`{"role":"user","parts":[`), part...), []byte(`]}`)...))
	}
	if errAppend != nil {
		return payload
	}
	return updated
}
