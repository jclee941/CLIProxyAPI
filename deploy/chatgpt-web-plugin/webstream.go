package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
)

// stream:true is answered through the host's stream bridge. The turn is opened
// before execute_stream returns, so a credential or upstream refusal still
// reaches the caller with its status; the reply is then relayed as
// chat.completion.chunk objects while it arrives. The host frames each chunk as
// SSE and writes the terminal [DONE] itself.

const webToolFence = "```tool_call"

type webRelay struct {
	service *service
	client  *webClient
	plan    webChatPlan
	id      string
	created int64
	text    string
	sent    string
	served  string
	gone    bool
}

func (service *service) executeChatStream(ctx context.Context, raw []byte) (interface{}, error) {
	plan, err := service.planChat(raw)
	if err != nil {
		return nil, err
	}
	if plan.stream == "" {
		return nil, failure(400, "stream_bridge_required")
	}
	turnCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	var lastErr error = failure(503, "web_chat_unavailable")
	for _, entry := range plan.candidates {
		client, err := service.clientFor(plan.callbackID, entry)
		if err != nil {
			lastErr = err
			continue
		}
		response, err := client.startReply(turnCtx, plan.turn)
		if err != nil {
			lastErr = err
			continue
		}
		relay := &webRelay{service: service, client: client, plan: plan, id: "chatcmpl-web-" + newDeviceID(), created: time.Now().Unix()}
		go relay.run(turnCtx, cancel, response)
		return struct {
			Headers http.Header `json:"headers"`
		}{http.Header{"Content-Type": {"text/event-stream"}}}, nil
	}
	cancel()
	return nil, lastErr
}

// run relays one turn. The opening chunk commits the response while the model
// may still be thinking. A caller that leaves surfaces as a refused emit, which
// ends the read at the next delta; either way the conversation the turn opened
// is deleted afterwards.
func (relay *webRelay) run(ctx context.Context, cancel context.CancelFunc, response *http.Response) {
	defer cancel()
	relay.gone = relay.emit(map[string]any{"role": "assistant", "content": ""}, nil) != nil
	reply, err := relay.client.finishReply(ctx, response, relay.plan.turn.Mode, relay.deliver)
	if err == nil {
		relay.service.checkServed(relay.plan.turn.Mode, reply)
		relay.served = reply.served()
		err = relay.finish(reply.Text)
	}
	// Closing tells a caller still listening how the turn ended; the bridge
	// refuses the close only for a caller that has already gone.
	_ = relay.service.streamCallback("host.stream.close", relay.plan.stream, nil, publicMessage(err))
	relay.service.discard(ctx, relay.client, reply.ConversationID)
}

// deliver passes on the part of the reply that is certain to be content. With
// tools on offer the reply may turn into a call block at any point, so content
// stops at the first sign of one and the rest waits for finish.
func (relay *webRelay) deliver(delta string) error {
	if relay.gone {
		return failure(499, "web_client_disconnected")
	}
	relay.text += delta
	ready := webShowable(relay.text, relay.plan.tools)
	piece, grew := strings.CutPrefix(ready, relay.sent)
	if !grew || piece == "" {
		return nil
	}
	relay.sent = ready
	return relay.emit(map[string]any{"content": piece}, nil)
}

// finish closes the turn with what the non-streaming path would have answered:
// the content not sent yet, any tool calls, and the finish reason.
func (relay *webRelay) finish(text string) error {
	content, calls := text, []webToolCall(nil)
	sent := relay.sent
	if relay.plan.tools {
		if clean, parsed := webParseToolCalls(text); len(parsed) > 0 {
			content, calls, sent = clean, parsed, strings.TrimLeftFunc(sent, unicode.IsSpace)
		}
	}
	if rest, ok := strings.CutPrefix(content, sent); ok && rest != "" {
		if err := relay.emit(map[string]any{"content": rest}, nil); err != nil {
			return err
		}
	}
	reason := "stop"
	if len(calls) > 0 {
		if err := relay.emit(map[string]any{"tool_calls": webRenderToolCalls(calls, true)}, nil); err != nil {
			return err
		}
		reason = "tool_calls"
	}
	return relay.emit(map[string]any{}, reason)
}

func (relay *webRelay) emit(delta map[string]any, finish any) error {
	body := map[string]any{
		"id":      relay.id,
		"object":  "chat.completion.chunk",
		"created": relay.created,
		"model":   relay.plan.model,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	}
	if relay.served != "" {
		body["system_fingerprint"] = relay.served
	}
	chunk, err := json.Marshal(body)
	if err != nil {
		return failure(500, "web_response_invalid")
	}
	return relay.service.streamCallback("host.stream.emit", relay.plan.stream, chunk, "")
}

// webShowable is the prefix of a reply that is certain to be content. With tools
// on offer, a reply opening with "{" may be a bare call and everything from a
// call fence on is a call; trailing characters that could begin a fence are held
// until the next delta settles them.
func webShowable(text string, tools bool) string {
	if !tools {
		return text
	}
	if strings.HasPrefix(strings.TrimLeftFunc(text, unicode.IsSpace), "{") {
		return ""
	}
	if index := strings.Index(text, webToolFence); index >= 0 {
		return text[:index]
	}
	for hold := min(len(webToolFence)-1, len(text)); hold > 0; hold-- {
		if strings.HasSuffix(text, webToolFence[:hold]) {
			return text[:len(text)-hold]
		}
	}
	return text
}

// streamCallback sends one frame, or the close, through the host bridge. The
// bridge refuses a stream whose caller has left, which is how a relay learns to
// stop.
func (service *service) streamCallback(method, stream string, payload []byte, message string) error {
	if service.host == nil {
		return failure(503, "host_stream_unavailable")
	}
	raw, err := json.Marshal(struct {
		StreamID string `json:"stream_id"`
		Payload  []byte `json:"payload,omitempty"`
		Error    string `json:"error,omitempty"`
	}{stream, payload, message})
	if err != nil {
		return failure(500, "stream_encoding_failed")
	}
	response, err := service.host(method, raw)
	if err != nil {
		return failure(499, "web_client_disconnected")
	}
	var result envelope
	if json.Unmarshal(response, &result) != nil || !result.OK {
		return failure(499, "web_client_disconnected")
	}
	return nil
}

// publicMessage keeps a fault that is not a public error opaque to the caller.
func publicMessage(err error) string {
	if err == nil {
		return ""
	}
	var public *publicError
	if errors.As(err, &public) {
		return public.Message
	}
	return "plugin_operation_failed"
}
