package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/tidwall/gjson"
)

// bufferStrictStream answers a streaming strict request from a complete,
// validated reply. A contract can only be judged once the whole reply is known,
// so the model is called without streaming and the enforced answer is framed as
// the event stream the caller asked for. Returning false leaves the request to
// the ordinary streaming path.
func bufferStrictStream(req pluginapi.RequestInterceptRequest, cfg pluginConfig, instructed []byte) (pluginapi.RequestInterceptResponse, bool) {
	request := nonStreaming(instructed)
	executed, ok := executeOnce(req.SourceFormat, req.Model, request)
	if !ok {
		return pluginapi.RequestInterceptResponse{}, false
	}
	enforced, _ := enforcement{
		spec:         parseSpec(request),
		tools:        parseTools(request),
		cfg:          cfg,
		sourceFormat: req.SourceFormat,
		model:        req.Model,
		request:      request,
	}.apply(executed)
	frames, ok := sseFrames(request, enforced)
	if !ok {
		return pluginapi.RequestInterceptResponse{}, false
	}
	return pluginapi.RequestInterceptResponse{
		Terminate:  true,
		StatusCode: http.StatusOK,
		ResponseHeaders: http.Header{
			"Content-Type":  []string{"text/event-stream"},
			"Cache-Control": []string{"no-cache"},
		},
		ResponseBody: frames,
	}, true
}

func sseFrames(request, response []byte) ([]byte, bool) {
	if gjson.GetBytes(request, "contents").IsArray() {
		return geminiFrames(response)
	}
	if gjson.GetBytes(request, "messages").IsArray() {
		return openAIFrames(response)
	}
	return nil, false
}

// geminiFrames carries the reply as a single event because Gemini streams whole
// GenerateContentResponse values rather than deltas, and ends without a sentinel.
func geminiFrames(response []byte) ([]byte, bool) {
	if !gjson.ValidBytes(response) || !gjson.GetBytes(response, "candidates").IsArray() {
		return nil, false
	}
	frame := append([]byte("data: "), response...)
	return append(frame, '\n', '\n'), true
}

type streamChunk struct {
	ID      string          `json:"id"`
	Object  string          `json:"object"`
	Created int64           `json:"created"`
	Model   string          `json:"model"`
	Choices []streamChoice  `json:"choices"`
	Usage   json.RawMessage `json:"usage,omitempty"`
}

type streamChoice struct {
	Index        int            `json:"index"`
	Delta        map[string]any `json:"delta"`
	FinishReason *string        `json:"finish_reason"`
}

// openAIFrames replays one completed chat completion as the two-chunk delta
// sequence a client expects: the whole answer, then the terminal reason.
func openAIFrames(response []byte) ([]byte, bool) {
	choice := gjson.GetBytes(response, "choices.0")
	if !choice.Exists() {
		return nil, false
	}
	base := streamChunk{
		ID:      firstNonEmpty(gjson.GetBytes(response, "id").String(), "chatcmpl-strict"),
		Object:  "chat.completion.chunk",
		Created: createdOrNow(gjson.GetBytes(response, "created").Int()),
		Model:   gjson.GetBytes(response, "model").String(),
	}
	delta := map[string]any{"role": "assistant"}
	if content := choice.Get("message.content"); content.Type == gjson.String {
		delta["content"] = content.String()
	}
	if calls := choice.Get("message.tool_calls"); calls.IsArray() {
		delta["tool_calls"] = json.RawMessage(calls.Raw)
	}
	finish := firstNonEmpty(choice.Get("finish_reason").String(), "stop")

	opening := base
	opening.Choices = []streamChoice{{Delta: delta}}
	closing := base
	closing.Choices = []streamChoice{{Delta: map[string]any{}, FinishReason: &finish}}
	if usage := gjson.GetBytes(response, "usage"); usage.IsObject() {
		closing.Usage = json.RawMessage(usage.Raw)
	}

	var frames []byte
	for _, chunk := range []streamChunk{opening, closing} {
		encoded, err := json.Marshal(chunk)
		if err != nil {
			return nil, false
		}
		frames = append(frames, "data: "...)
		frames = append(frames, encoded...)
		frames = append(frames, '\n', '\n')
	}
	return append(frames, "data: [DONE]\n\n"...), true
}

func createdOrNow(created int64) int64 {
	if created > 0 {
		return created
	}
	return time.Now().Unix()
}
