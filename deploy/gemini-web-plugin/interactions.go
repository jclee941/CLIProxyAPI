package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const interactionOmniModel = "gemini-omni-1.1-flash"

type interactionRequest struct {
	Model          string          `json:"model"`
	Input          json.RawMessage `json:"input"`
	Previous       string          `json:"previous_interaction_id,omitempty"`
	Store          *bool           `json:"store,omitempty"`
	Background     bool            `json:"background,omitempty"`
	Stream         bool            `json:"stream,omitempty"`
	ResponseFormat struct {
		Type        string `json:"type,omitempty"`
		AspectRatio string `json:"aspect_ratio,omitempty"`
		Resolution  string `json:"resolution,omitempty"`
		Delivery    string `json:"delivery,omitempty"`
	} `json:"response_format,omitempty"`
}

func parseInteraction(raw []byte) (interactionRequest, []byte, error) {
	var request interactionRequest
	if len(raw) > 40*1024 || strictJSON(raw, &request) != nil || request.Model != interactionOmniModel {
		return request, nil, failure(400, "unsupported_interaction_request")
	}
	if request.Background {
		return request, nil, failure(400, "interaction_background_unsupported")
	}
	format := request.ResponseFormat
	if format.Type != "" && format.Type != "video" || format.Delivery != "" && format.Delivery != "inline" {
		return request, nil, failure(400, "interaction_inline_video_only")
	}
	if format.Resolution != "" {
		return request, nil, failure(400, "interaction_resolution_unsupported")
	}
	var prompt string
	if json.Unmarshal(request.Input, &prompt) != nil {
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if strictJSON(request.Input, &parts) != nil || len(parts) == 0 {
			return request, nil, failure(400, "interaction_text_input_only")
		}
		texts := make([]string, 0, len(parts))
		for _, part := range parts {
			if part.Type != "text" {
				return request, nil, failure(400, "interaction_text_input_only")
			}
			texts = append(texts, part.Text)
		}
		prompt = strings.Join(texts, "\n")
	}
	payload, err := json.Marshal(map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]string{"text": prompt}}}}, "generationConfig": map[string]string{"aspectRatio": format.AspectRatio}})
	if err != nil {
		return request, nil, err
	}
	if format.AspectRatio == "" {
		payload, err = omniGeminiPayload(prompt, omniOptions{})
		if err != nil {
			return request, nil, err
		}
	}
	if err := validateOmni(payload); err != nil {
		return request, nil, err
	}
	return request, payload, nil
}

// Standard Interactions create is a new turn. Retrieval is deliberately NOT
// emulated with a second create: the host must provide GET /interactions/{id}.
func (service *service) executeInteraction(ctx context.Context, request executorRequest) (interface{}, error) {
	if !accountDigestPattern.MatchString(request.Metadata.CallerScope) {
		return nil, failure(400, "interaction_requires_authenticated_caller_scope")
	}
	if !service.settings().NativeContinuation || !service.settings().NativeGeneration {
		return nil, failure(400, "native_continuation_disabled")
	}
	if request.Model != interactionOmniModel || request.SourceFormat != "interactions" || request.Alt != "" && request.Alt != interactionRetrieveAlt {
		return nil, failure(400, "unsupported_interaction_route")
	}
	if request.Alt == interactionRetrieveAlt {
		return service.retrieveInteraction(ctx, request)
	}
	body, payload, err := parseInteraction(request.Payload)
	if err != nil {
		return nil, err
	}
	native := request
	native.Model, native.Format, native.SourceFormat = omniModel, "gemini", "gemini"
	native.Stream = false
	native.freshContinuation = true
	native.Payload, err = json.Marshal(map[string]any{continuationField: continuationControl{Action: "prepare", Token: body.Previous}})
	if err != nil {
		return nil, err
	}
	prepared, err := service.executeContinuation(ctx, native)
	if err != nil {
		return nil, err
	}
	result := prepared.(continuationResult)
	var receipt struct {
		View continuationView `json:"geminiWebContinuation"`
	}
	if err := json.Unmarshal(result.Payload, &receipt); err != nil {
		return nil, err
	}
	token := receipt.View.Token
	var upstream map[string]json.RawMessage
	if err := json.Unmarshal(payload, &upstream); err != nil {
		return nil, err
	}
	upstream[continuationField], err = json.Marshal(continuationControl{Action: "submit", Token: token})
	if err != nil {
		return nil, err
	}
	native.Payload, err = json.Marshal(upstream)
	if err != nil {
		return nil, err
	}
	if request.Stream {
		return service.startInteractionStream(ctx, request, native, token, body.Store)
	}
	return service.finishInteraction(ctx, native, token, body.Store)
}

func (service *service) finishInteraction(ctx context.Context, native executorRequest, token string, store *bool) (interface{}, error) {
	var receipt struct {
		View continuationView `json:"geminiWebContinuation"`
	}
	var result continuationResult
	record, err := service.parseStorage(native.StorageJSON, false)
	if err != nil {
		return nil, err
	}
	deadline := service.now().Add(webVideoBudget)
	for {
		executed, err := service.executeContinuation(ctx, native)
		if err != nil {
			return nil, err
		}
		result = executed.(continuationResult)
		// Video renewal can advance the durable projection during this request.
		// Subsequent observations must use that revision, not the incoming one.
		latest, err := service.localStore().read(record.TokenRef)
		if err != nil {
			return nil, err
		}
		native.StorageJSON = []byte(latest.Projection)
		if err := json.Unmarshal(result.Payload, &receipt); err != nil {
			return nil, err
		}
		if receipt.View.State != "pending" || receipt.View.Error != "" || !service.now().Before(deadline) {
			break
		}
		if err := service.waitInteraction(ctx); err != nil {
			return nil, err
		}
		native.Payload, err = json.Marshal(map[string]any{continuationField: continuationControl{Action: "recover", Token: token}})
		if err != nil {
			return nil, err
		}
	}
	if store != nil && !*store && receipt.View.State == "complete" {
		if err := service.discardInteraction(native, token); err != nil {
			return nil, err
		}
	}
	return renderInteraction(result, receipt.View)
}

func (service *service) waitInteraction(ctx context.Context) error {
	if service.continuationWait != nil {
		return service.continuationWait(ctx)
	}
	timer := time.NewTimer(webVideoPollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func renderInteraction(result continuationResult, view continuationView) (interface{}, error) {
	status := "in_progress"
	steps := make([]any, 0, 1)
	if view.State == "complete" {
		status = "completed"
		var native struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						InlineData struct {
							MIMEType string `json:"mimeType"`
							Data     string `json:"data"`
						} `json:"inlineData"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if json.Unmarshal(result.Payload, &native) != nil || len(native.Candidates) != 1 || len(native.Candidates[0].Content.Parts) != 1 {
			return nil, failure(502, "invalid_interaction_video")
		}
		video := native.Candidates[0].Content.Parts[0].InlineData
		if video.MIMEType != "video/mp4" || video.Data == "" {
			return nil, failure(502, "invalid_interaction_video")
		}
		steps = append(steps, map[string]any{"type": "model_output", "content": []any{map[string]string{"type": "video", "mime_type": video.MIMEType, "data": video.Data}}})
	}
	if view.State == "outcome_unknown" {
		status = "failed"
	}
	payload, err := json.Marshal(map[string]any{"id": view.Token, "object": "interaction", "model": interactionOmniModel, "status": status, "steps": steps})
	if err != nil {
		return nil, err
	}
	return continuationResult{Payload: payload, Headers: http.Header{"Content-Type": {"application/json"}}}, nil
}

func (service *service) discardInteraction(request executorRequest, token string) error {
	record, err := service.parseStorage(request.StorageJSON, false)
	if err != nil {
		return err
	}
	lease, err := service.accountLease(record)
	if err != nil {
		return err
	}
	if !lease.guard.TryLock() {
		return failure(409, "session_busy")
	}
	defer lease.guard.Unlock()
	local, err := service.localStore().read(record.TokenRef)
	if err != nil {
		return err
	}
	turns, err := continuationTurns(local)
	if err != nil {
		return err
	}
	delete(turns, continuationKey(token))
	return service.saveContinuations(local, turns)
}
