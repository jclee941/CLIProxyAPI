package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/structuredoutput"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// structuredOutputHints remembers which models have already broken their
// response_format contract, so those models are instructed up front on later
// requests instead of paying a wasted round trip every time. Models that honour
// the contract are never recorded and therefore never see an altered prompt.
var structuredOutputHints sync.Map

// ExecuteStructuredOutput runs a non-streaming request and holds the client's
// response_format contract even when the upstream ignores it.
//
// Providers with native structured output are unaffected: their first reply
// already satisfies the schema, so validation passes untouched and no retry or
// prompt change occurs. Providers that ignore the field are corrected with the
// specific violations until they comply or the attempt budget is exhausted.
func (h *BaseAPIHandler) ExecuteStructuredOutput(ctx context.Context, handlerType, modelName string, rawJSON []byte, alt string) ([]byte, http.Header, *interfaces.ErrorMessage) {
	spec := structuredoutput.Parse(rawJSON)
	if spec == nil || !gjson.GetBytes(rawJSON, "messages").IsArray() {
		return h.ExecuteWithAuthManager(ctx, handlerType, modelName, rawJSON, alt)
	}

	payload := rawJSON
	if _, known := structuredOutputHints.Load(modelName); known {
		payload = withSystemInstruction(payload, structuredoutput.InstructionText(spec))
	}

	var problems []string
	for attempt := 0; attempt < structuredoutput.MaxAttempts; attempt++ {
		body, headers, errMsg := h.ExecuteWithAuthManager(ctx, handlerType, modelName, payload, alt)
		if errMsg != nil {
			return nil, nil, errMsg
		}

		content := gjson.GetBytes(body, "choices.0.message.content")
		if !content.Exists() {
			return body, headers, nil
		}

		cleaned, violations := structuredoutput.Coerce(content.String(), spec)
		if len(violations) == 0 {
			if cleaned != content.String() {
				if rewritten, err := sjson.SetBytes(body, "choices.0.message.content", cleaned); err == nil {
					body = rewritten
				}
			}
			return body, headers, nil
		}

		problems = violations
		structuredOutputHints.Store(modelName, struct{}{})
		if attempt == 0 {
			payload = withSystemInstruction(payload, structuredoutput.InstructionText(spec))
		}
		payload = appendMessage(payload, "assistant", content.String())
		payload = appendMessage(payload, "user", structuredoutput.CorrectionText(violations))
	}

	return nil, nil, &interfaces.ErrorMessage{
		StatusCode: http.StatusUnprocessableEntity,
		Error:      fmt.Errorf("the reply never satisfied response_format: %s", joinProblems(problems)),
	}
}

// RequiresStructuredOutput reports whether a request asks for structured output
// that this handler can hold on the client's behalf.
func RequiresStructuredOutput(rawJSON []byte) bool {
	return structuredoutput.Parse(rawJSON) != nil && gjson.GetBytes(rawJSON, "messages").IsArray()
}

// withSystemInstruction prepends a system message stating the output contract.
func withSystemInstruction(payload []byte, text string) []byte {
	instruction, err := json.Marshal(map[string]string{"role": "system", "content": text})
	if err != nil {
		return payload
	}
	messages := []json.RawMessage{instruction}
	gjson.GetBytes(payload, "messages").ForEach(func(_, value gjson.Result) bool {
		messages = append(messages, json.RawMessage(value.Raw))
		return true
	})
	encoded, err := json.Marshal(messages)
	if err != nil {
		return payload
	}
	updated, err := sjson.SetRawBytes(payload, "messages", encoded)
	if err != nil {
		return payload
	}
	return updated
}

// appendMessage adds one message to the end of the conversation.
func appendMessage(payload []byte, role, content string) []byte {
	message, err := json.Marshal(map[string]string{"role": role, "content": content})
	if err != nil {
		return payload
	}
	updated, err := sjson.SetRawBytes(payload, "messages.-1", message)
	if err != nil {
		return payload
	}
	return updated
}

func joinProblems(problems []string) string {
	if len(problems) > 5 {
		problems = problems[:5]
	}
	joined := ""
	for index, problem := range problems {
		if index > 0 {
			joined += "; "
		}
		joined += problem
	}
	return joined
}
