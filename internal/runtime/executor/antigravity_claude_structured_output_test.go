package executor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	translatorcommon "github.com/router-for-me/CLIProxyAPI/v8/internal/translator/common"
	"github.com/tidwall/gjson"
)

const antigravityTestResponseSchema = `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`

var antigravityTestSchemaKeys = []string{"responseSchema", "responseJsonSchema", "response_schema", "response_json_schema"}

func antigravityTestStructuredOutputInstruction() string {
	return translatorcommon.BuildClaudeStructuredOutputInstruction(
		gjson.Parse(`{"type":"json_schema","json_schema":{"schema":` + antigravityTestResponseSchema + `}}`),
	)
}

// antigravityStructuredOutputPayload builds an Antigravity request. Declaring a tool selects the
// sanitized branch of buildRequest; omitting it selects the non-sanitized branch.
func antigravityStructuredOutputPayload(container, schemaKey string, withTools, withSystem bool) []byte {
	var generationConfig strings.Builder
	generationConfig.WriteString(`{"temperature":0.2,"responseMimeType":"application/json"`)
	if schemaKey != "" {
		generationConfig.WriteString(`,"` + schemaKey + `":` + antigravityTestResponseSchema)
	}
	generationConfig.WriteString(`}`)

	var request strings.Builder
	request.WriteString(`{"request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}]`)
	if withSystem {
		request.WriteString(`,"systemInstruction":{"role":"user","parts":[{"text":"be brief"}]}`)
	}
	if withTools {
		request.WriteString(`,"tools":[{"functionDeclarations":[{"name":"lookup","parametersJsonSchema":{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}}]}]`)
	}
	request.WriteString(`,"` + container + `":` + generationConfig.String())
	request.WriteString(`,"unknownField":{"keep":true}}}`)
	return []byte(request.String())
}

func encodeAntigravityBody(t *testing.T, body map[string]any) []byte {
	t.Helper()
	encoded, errMarshal := json.Marshal(body)
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	return encoded
}

func TestAntigravityBuildRequestClaudeResponseSchemaBecomesSystemInstruction(t *testing.T) {
	wantInstruction := antigravityTestStructuredOutputInstruction()
	if wantInstruction == "" {
		t.Fatal("structured-output instruction must not be empty")
	}

	for _, modelName := range []string{"claude-sonnet-5-5-high", "claude-sonnet-4-6"} {
		for _, container := range []string{"generationConfig", "generation_config"} {
			for _, schemaKey := range antigravityTestSchemaKeys {
				for _, withTools := range []bool{false, true} {
					for _, withSystem := range []bool{false, true} {
						name := strings.Join([]string{modelName, container, schemaKey, branchName(withTools), systemName(withSystem)}, "/")
						t.Run(name, func(t *testing.T) {
							payload := antigravityStructuredOutputPayload(container, schemaKey, withTools, withSystem)
							encoded := encodeAntigravityBody(t, buildRequestBodyFromRawPayload(t, modelName, payload))

							for _, genContainer := range []string{"generationConfig", "generation_config"} {
								for _, key := range antigravityTestSchemaKeys {
									if gjson.GetBytes(encoded, "request."+genContainer+"."+key).Exists() {
										t.Fatalf("schema key %s.%s survived: %s", genContainer, key, encoded)
									}
								}
							}
							generationConfig := gjson.GetBytes(encoded, "request."+container)
							if got := generationConfig.Get("responseMimeType").String(); got != "application/json" {
								t.Fatalf("responseMimeType = %q, want application/json: %s", got, encoded)
							}
							if got := generationConfig.Get("temperature").Float(); got != 0.2 {
								t.Fatalf("temperature = %v, want 0.2: %s", got, encoded)
							}
							if !gjson.GetBytes(encoded, "request.unknownField.keep").Bool() {
								t.Fatalf("unknown field was dropped: %s", encoded)
							}

							parts := gjson.GetBytes(encoded, "request.systemInstruction.parts").Array()
							wantParts := 1
							if withSystem {
								wantParts = 2
								if got := parts[0].Get("text").String(); got != "be brief" {
									t.Fatalf("existing system part = %q, want unchanged: %s", got, encoded)
								}
							}
							if len(parts) != wantParts {
								t.Fatalf("system parts = %d, want %d: %s", len(parts), wantParts, encoded)
							}
							if got := parts[len(parts)-1].Get("text").String(); got != wantInstruction {
								t.Fatalf("appended instruction mismatch:\n got: %q\nwant: %q", got, wantInstruction)
							}
							if got := gjson.GetBytes(encoded, "request.systemInstruction.role").String(); got != "user" {
								t.Fatalf("systemInstruction.role = %q, want user: %s", got, encoded)
							}
							if got := gjson.GetBytes(encoded, "request.toolConfig.functionCallingConfig.mode").String(); got != "VALIDATED" {
								t.Fatalf("Claude VALIDATED override missing: %s", encoded)
							}
						})
					}
				}
			}
		}
	}
}

func TestAntigravityBuildRequestClaudeWithoutResponseSchemaIsUnchanged(t *testing.T) {
	for _, withTools := range []bool{false, true} {
		for _, withSystem := range []bool{false, true} {
			t.Run(strings.Join([]string{branchName(withTools), systemName(withSystem)}, "/"), func(t *testing.T) {
				payload := antigravityStructuredOutputPayload("generationConfig", "", withTools, withSystem)
				if got := helps.ApplyAntigravityClaudeStructuredOutput(payload); string(got) != string(payload) {
					t.Fatalf("helper changed a payload without a schema:\n got: %s\nwant: %s", got, payload)
				}

				encoded := encodeAntigravityBody(t, buildRequestBodyFromRawPayload(t, "claude-sonnet-5-5-high", payload))
				if got := gjson.GetBytes(encoded, "request.generationConfig.responseMimeType").String(); got != "application/json" {
					t.Fatalf("responseMimeType = %q, want application/json: %s", got, encoded)
				}
				parts := gjson.GetBytes(encoded, "request.systemInstruction.parts").Array()
				if withSystem {
					if len(parts) != 1 || parts[0].Get("text").String() != "be brief" {
						t.Fatalf("system instruction changed: %s", encoded)
					}
				} else if gjson.GetBytes(encoded, "request.systemInstruction").Exists() {
					t.Fatalf("system instruction was created without a schema: %s", encoded)
				}
			})
		}
	}
}

func TestAntigravityBuildRequestGeminiKeepsResponseSchema(t *testing.T) {
	for _, modelName := range []string{"gemini-3.1-pro-low", "gemini-2.5-flash"} {
		for _, withTools := range []bool{false, true} {
			for _, withSystem := range []bool{false, true} {
				t.Run(strings.Join([]string{modelName, branchName(withTools), systemName(withSystem)}, "/"), func(t *testing.T) {
					payload := antigravityStructuredOutputPayload("generationConfig", "responseSchema", withTools, withSystem)
					encoded := encodeAntigravityBody(t, buildRequestBodyFromRawPayload(t, modelName, payload))

					schema := gjson.GetBytes(encoded, "request.generationConfig.responseSchema")
					if !schema.Get("properties.answer").Exists() {
						t.Fatalf("Gemini response schema was removed: %s", encoded)
					}
					if got := gjson.GetBytes(encoded, "request.generationConfig.responseMimeType").String(); got != "application/json" {
						t.Fatalf("responseMimeType = %q, want application/json: %s", got, encoded)
					}
					parts := gjson.GetBytes(encoded, "request.systemInstruction.parts").Array()
					if withSystem {
						if len(parts) != 1 || parts[0].Get("text").String() != "be brief" {
							t.Fatalf("Gemini system instruction changed: %s", encoded)
						}
					} else if gjson.GetBytes(encoded, "request.systemInstruction").Exists() {
						t.Fatalf("Gemini system instruction was created: %s", encoded)
					}
				})
			}
		}
	}
}

func TestApplyAntigravityClaudeStructuredOutputPreservesKeyOrder(t *testing.T) {
	payload := []byte(`{"request":{"zeta":1,"generationConfig":{"temperature":0.2,"responseSchema":` + antigravityTestResponseSchema + `,"responseMimeType":"application/json"},"alpha":2}}`)
	got := helps.ApplyAntigravityClaudeStructuredOutput(payload)

	if gjson.GetBytes(got, "request.generationConfig").Raw != `{"temperature":0.2,"responseMimeType":"application/json"}` {
		t.Fatalf("generationConfig = %s", gjson.GetBytes(got, "request.generationConfig").Raw)
	}
	if !strings.HasPrefix(string(got), `{"request":{"zeta":1,"generationConfig":`) || !strings.Contains(string(got), `},"alpha":2`) {
		t.Fatalf("surrounding key order changed: %s", got)
	}
}

func branchName(withTools bool) string {
	if withTools {
		return "sanitized"
	}
	return "unsanitized"
}

func systemName(withSystem bool) string {
	if withSystem {
		return "with-system"
	}
	return "no-system"
}
