package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
)

type interactionOperation struct {
	done   chan struct{}
	result continuationResult
	err    error
}

// Generation belongs to the plugin lifecycle, not to any SSE subscriber. GET
// can join this operation; after restart it can only run continuation recover.
func (service *service) ownInteraction(ctx context.Context, native executorRequest, token string, store *bool, background bool) (*interactionOperation, error) {
	service.interactionsMu.Lock()
	defer service.interactionsMu.Unlock()
	if operation := service.interactions[token]; operation != nil {
		return operation, nil
	}
	if err := service.lifecycle.enter(); err != nil {
		return nil, err
	}
	operation := &interactionOperation{done: make(chan struct{})}
	if service.interactions == nil {
		service.interactions = make(map[string]*interactionOperation)
	}
	service.interactions[token] = operation
	owned, cancel := context.WithCancel(context.WithoutCancel(ctx))
	closing := service.lifecycle.closingSignal()
	go func() {
		select {
		case <-closing:
			cancel()
		case <-owned.Done():
		}
	}()
	go func() {
		defer service.lifecycle.leave()
		defer cancel()
		result, err := service.finishInteraction(owned, native, token, store)
		if background {
			result, err = service.persistBackgroundOutcome(native, token, result, err)
			if err != nil {
				log.Printf("gemini-web: background interaction ended: %s", safeCredentialCode(err))
			}
		}
		if err == nil {
			operation.result = result.(continuationResult)
		}
		operation.err = err
		service.interactionsMu.Lock()
		delete(service.interactions, token)
		close(operation.done)
		service.interactionsMu.Unlock()
	}()
	return operation, nil
}

func (service *service) startInteractionStream(ctx context.Context, request, native executorRequest, token string, store *bool, background bool) (interface{}, error) {
	if request.StreamID == "" {
		return nil, failure(400, "interaction_stream_bridge_required")
	}
	operation, err := service.ownInteraction(ctx, native, token, store, background)
	if err != nil {
		return nil, err
	}
	return service.subscribeInteraction(request.StreamID, token, 0, operation)
}

func interactionCursor(token, cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	prefix := token + ":"
	if !strings.HasPrefix(cursor, prefix) {
		return 0, failure(400, "invalid_interaction_event_id")
	}
	n, err := strconv.Atoi(strings.TrimPrefix(cursor, prefix))
	if err != nil || n < 1 || n > 6 || cursor != fmt.Sprintf("%s:%d", token, n) {
		return 0, failure(400, "invalid_interaction_event_id")
	}
	return n, nil
}

func interactionEvent(token string, number int, name string, body map[string]any) ([]byte, error) {
	id := fmt.Sprintf("%s:%d", token, number)
	var data []byte
	var err error
	if name == "done" {
		data = []byte("[DONE]")
	} else {
		body["event_type"], body["event_id"] = name, id
		data, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	// Start with event: for native SSE forwarding; id: must not be wrapped as data.
	return fmt.Appendf(nil, "event: %s\nid: %s\ndata: %s\n\n", name, id, data), nil
}

func (service *service) streamInteractionCallback(method, stream string, payload []byte, message string) error {
	raw, err := json.Marshal(struct {
		StreamID string `json:"stream_id"`
		Payload  []byte `json:"payload,omitempty"`
		Error    string `json:"error,omitempty"`
	}{stream, payload, message})
	if err != nil {
		return err
	}
	response, err := service.host(method, raw)
	if err != nil {
		return err
	}
	var result envelope
	if json.Unmarshal(response, &result) != nil || !result.OK {
		return failure(503, "interaction_subscriber_disconnected")
	}
	return nil
}

// emitInteractionError writes an outcome as an SSE error event carrying the
// interaction, in the shape the host gives a failed close, so that the stream
// can still close cleanly afterwards.
func (service *service) emitInteractionError(stream, message string, interaction any) error {
	payload, err := json.Marshal(map[string]any{
		"error": map[string]string{
			"message": message,
			"type":    "server_error",
			"code":    "internal_server_error",
		},
		"interaction": interaction,
	})
	if err != nil {
		return err
	}
	return service.streamInteractionCallback("host.stream.emit", stream, fmt.Appendf(nil, "event: error\ndata: %s\n\n", payload), "")
}

func (service *service) subscribeInteraction(stream, token string, cursor int, operation *interactionOperation) (interface{}, error) {
	if err := service.lifecycle.enter(); err != nil {
		return nil, err
	}
	go func() {
		defer service.lifecycle.leave()
		err := service.sendInteractionEvents(stream, token, cursor, operation)
		message := ""
		if err != nil {
			message = safeCredentialMessage(err)
		}
		// A disconnected subscriber is not a generation failure. The owned operation
		// continues, and its result is recoverable through the durable receipt.
		if err := service.streamInteractionCallback("host.stream.close", stream, nil, message); err != nil {
			log.Printf("gemini-web: interaction subscriber close failed: %s", safeCredentialCode(err))
		}
	}()
	return struct{ Headers http.Header }{http.Header{"Content-Type": {"text/event-stream"}}}, nil
}

func (service *service) sendInteractionEvents(stream, token string, cursor int, operation *interactionOperation) error {
	emit := func(n int, name string, body map[string]any) error {
		if n <= cursor {
			return nil
		}
		payload, err := interactionEvent(token, n, name, body)
		if err != nil {
			return err
		}
		return service.streamInteractionCallback("host.stream.emit", stream, payload, "")
	}
	if cursor > 0 {
		// Establish native SSE before waiting or closing a resumed replay, so
		// the host bootstrap cannot retry its pinned account on an empty stream.
		// Comments carry no event or cursor, including for a completed replay.
		if err := service.streamInteractionCallback("host.stream.emit", stream, []byte(": replay\n\n"), ""); err != nil {
			return err
		}
	}
	if err := emit(1, "interaction.created", map[string]any{"interaction": map[string]any{"id": token, "object": "interaction", "model": interactionOmniModel, "status": "in_progress", "steps": []any{}}}); err != nil {
		return err
	}
	select {
	case <-operation.done:
	case <-service.lifecycle.closingSignal():
		return failure(503, "plugin_shutdown")
	}
	if operation.err != nil {
		if safeCredentialCode(operation.err) != "no_video_generated" {
			return operation.err
		}
		// A turn the product answered without a video is an outcome, not a
		// transport fault. The Interactions object has a status field for
		// exactly this, so the outcome is stated there, and the error event a
		// subscriber may be watching for follows as data. The stream then
		// closes cleanly: a close carrying the refusal was recorded by the host
		// as a credential failure, so the account sat out a sixty second
		// cooldown and the caller's retry of a chain that has to stay on it was
		// refused as continuation_account_unavailable without reaching it.
		failed := map[string]any{
			"id": token, "object": "interaction", "model": interactionOmniModel,
			"status": "failed", "steps": []any{},
			"error": map[string]string{"code": "no_video_generated", "message": safeCredentialMessage(operation.err)},
		}
		if err := emit(2, "interaction.failed", map[string]any{"interaction": failed}); err != nil {
			return err
		}
		return service.emitInteractionError(stream, safeCredentialMessage(operation.err), failed)
	}
	var result struct {
		Status string                   `json:"status"`
		Error  struct{ Message string } `json:"error"`
		Steps  []struct {
			Content []json.RawMessage `json:"content"`
		} `json:"steps"`
	}
	if json.Unmarshal(operation.result.Payload, &result) != nil {
		return failure(502, "invalid_interaction_video")
	}
	if result.Status == "in_progress" || result.Status == "failed" {
		message := "interaction_pending_retrieve_receipt"
		if result.Status == "failed" {
			if result.Error.Message == "" {
				return failure(502, "invalid_interaction_video")
			}
			if err := emit(2, "interaction.failed", map[string]any{"interaction": json.RawMessage(operation.result.Payload)}); err != nil {
				return err
			}
			message = result.Error.Message
		}
		// Preserve outcome/error events as SSE data, not a stream-close error
		// that the host records as a credential failure. Retain diagnostics.
		return service.emitInteractionError(stream, message, json.RawMessage(operation.result.Payload))
	}
	if result.Status != "completed" {
		return failure(409, "interaction_pending_retrieve_receipt")
	}
	if cursor == 6 {
		return nil
	}
	if len(result.Steps) != 1 || len(result.Steps[0].Content) != 1 {
		return failure(502, "invalid_interaction_video")
	}
	events := []struct {
		Name string
		Body map[string]any
	}{
		{"step.start", map[string]any{"index": 0, "step": map[string]string{"type": "model_output"}}},
		{"step.delta", map[string]any{"index": 0, "delta": result.Steps[0].Content[0]}},
		{"step.stop", map[string]any{"index": 0}},
		{"interaction.completed", map[string]any{"interaction": json.RawMessage(operation.result.Payload)}},
		{"done", nil},
	}
	for i, event := range events {
		if err := emit(i+2, event.Name, event.Body); err != nil {
			return err
		}
	}
	return nil
}
