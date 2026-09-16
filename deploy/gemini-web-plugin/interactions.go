package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const interactionOmniModel = "gemini-omni-1.1-flash"

// interactionInputLimit bounds the create body. Reference images travel inline
// as base64, so the text-sized bound this route started with cannot hold them.
// The prompt stays bounded at 8000 runes and the payload this builds is bounded
// again at 5MB, both by omniRequest.
const interactionInputLimit = 4 * 1024 * 1024

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
	if len(raw) > interactionInputLimit || strictJSON(raw, &request) != nil || request.Model != interactionOmniModel {
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
	var references []webMedia
	if json.Unmarshal(request.Input, &prompt) != nil {
		var parts []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Data     string `json:"data"`
			MIMEType string `json:"mime_type"`
			URI      string `json:"uri"`
		}
		if strictJSON(request.Input, &parts) != nil || len(parts) == 0 {
			return request, nil, failure(400, "interaction_text_input_only")
		}
		texts := make([]string, 0, len(parts))
		for _, part := range parts {
			switch part.Type {
			case "text":
				texts = append(texts, part.Text)
			case "image", "video":
				// A uri names either a Drive file, which is fetched, or a Files
				// entry, which has no API on this path to resolve it against.
				if part.URI != "" {
					if _, ok := driveFileID(part.URI); !ok {
						return request, nil, failure(400, "interaction_uploaded_reference_unsupported")
					}
					references = append(references, webMedia{Reference: part.URI})
					continue
				}
				if part.Data == "" || part.MIMEType == "" {
					return request, nil, failure(400, "interaction_reference_invalid")
				}
				references = append(references, webMedia{MIMEType: part.MIMEType, Data: part.Data})
			default:
				return request, nil, failure(400, "interaction_text_input_only")
			}
		}
		prompt = strings.Join(texts, "\n")
	}
	parts := make([]any, 0, len(references)+1)
	parts = append(parts, map[string]string{"text": prompt})
	for _, reference := range references {
		if reference.Reference != "" {
			parts = append(parts, map[string]any{"fileData": map[string]string{"fileUri": reference.Reference}})
			continue
		}
		parts = append(parts, map[string]any{"inlineData": map[string]string{"mimeType": reference.MIMEType, "data": reference.Data}})
	}
	content := map[string]any{"contents": []any{map[string]any{"role": "user", "parts": parts}}}
	if format.AspectRatio != "" {
		content["generationConfig"] = map[string]string{"aspectRatio": format.AspectRatio}
	}
	payload, err := json.Marshal(content)
	if err != nil {
		return request, nil, err
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
	// A chain continues the conversation when the account serving it is the one
	// that holds it, and carries the video as a reference when it is not. Either
	// way the caller names only the interaction it is continuing.
	previous := body.Previous
	if previous != "" {
		location, carried, locateErr := service.locateChained(request, previous)
		if locateErr != nil {
			return nil, locateErr
		}
		if carried {
			if payload, err = withChainedReference(payload, location); err != nil {
				return nil, err
			}
			previous = ""
		}
	}
	native.Payload, err = json.Marshal(map[string]any{continuationField: continuationControl{Action: "prepare", Token: previous}})
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
	return renderInteraction(record.ID, result, receipt.View)
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

func renderInteraction(account string, result continuationResult, view continuationView) (interface{}, error) {
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
	body := map[string]any{"id": view.Token, "object": "interaction", "model": interactionOmniModel, "account": account, "status": status, "steps": steps}
	// A failure the plugin can name was being dropped here, so a caller saw only
	// that the turn failed and had nothing to act on - the same blank answer
	// whether the product declined the prompt, ran out of daily video, or
	// returned something unreadable.
	if view.Error != "" {
		body["error"] = map[string]string{"code": view.Error, "message": view.Error}
	}
	payload, err := json.Marshal(body)
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
