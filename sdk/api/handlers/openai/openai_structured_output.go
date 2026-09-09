package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	"github.com/tidwall/gjson"
)

// streamStructuredOutput answers a streaming request that carries a
// response_format contract.
//
// A schema can only be checked once the whole reply is known, so the reply is
// produced non-streaming, validated, and then emitted as a normal SSE sequence.
// Clients see the usual chunk shape; only the token-by-token pacing is lost, and
// only for requests that asked for structured output.
func (h *OpenAIAPIHandler) streamStructuredOutput(c *gin.Context, flusher http.Flusher, rawJSON []byte) {
	modelName := gjson.GetBytes(rawJSON, "model").String()
	cliCtx, cliCancel := h.GetContextWithCancel(h, c, context.Background())

	body, upstreamHeaders, errMsg := h.ExecuteStructuredOutput(cliCtx, h.HandlerType(), modelName, rawJSON, h.GetAlt(c))
	if errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		cliCancel(errMsg.Error)
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)

	for _, chunk := range structuredOutputChunks(body, modelName) {
		_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", chunk)
	}
	_, _ = fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	flusher.Flush()
	cliCancel(nil)
}

// structuredOutputChunks converts a chat completion into the chunk pair a
// streaming client expects: one carrying the content, one closing the choice.
func structuredOutputChunks(body []byte, modelName string) []string {
	id := gjson.GetBytes(body, "id").String()
	if id == "" {
		id = "chatcmpl-structured"
	}
	created := gjson.GetBytes(body, "created").Int()
	if created == 0 {
		created = time.Now().Unix()
	}
	if model := gjson.GetBytes(body, "model").String(); model != "" {
		modelName = model
	}
	content := gjson.GetBytes(body, "choices.0.message.content").String()

	build := func(delta map[string]any, finishReason any) string {
		chunk := map[string]any{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   modelName,
			"choices": []map[string]any{{
				"index":         0,
				"delta":         delta,
				"finish_reason": finishReason,
			}},
		}
		encoded, err := json.Marshal(chunk)
		if err != nil {
			return "{}"
		}
		return string(encoded)
	}

	return []string{
		build(map[string]any{"role": "assistant", "content": content}, nil),
		build(map[string]any{}, "stop"),
	}
}
