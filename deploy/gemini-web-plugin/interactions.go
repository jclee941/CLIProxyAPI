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
	GenerationConfig struct {
		VideoConfig struct {
			Task string `json:"task,omitempty"`
		} `json:"video_config,omitempty"`
	} `json:"generation_config,omitempty"`
}

// interactionExtendTask is the documented task that continues a video instead of
// starting one. It is the only task this surface can honour: the product has no
// slot for the others, and an uploaded video declared as an edit source was
// measured to spend the whole budget and answer no video.
const interactionExtendTask = "extend"

func parseInteraction(raw []byte) (interactionRequest, []byte, error) {
	var request interactionRequest
	if len(raw) > interactionInputLimit || strictJSON(raw, &request) != nil || request.Model != interactionOmniModel {
		return request, nil, failure(400, "unsupported_interaction_request")
	}
	if request.Background && request.Store != nil && !*request.Store {
		return request, nil, failure(400, "interaction_background_requires_store")
	}
	format := request.ResponseFormat
	if format.Type != "" && format.Type != "video" || format.Delivery != "" && format.Delivery != "inline" && format.Delivery != "uri" {
		return request, nil, failure(400, "interaction_inline_video_only")
	}
	if format.Resolution != "" && format.Resolution != "720p" {
		return request, nil, failure(400, "interaction_resolution_unsupported")
	}
	if task := request.GenerationConfig.VideoConfig.Task; task != "" && task != interactionExtendTask {
		return request, nil, failure(400, "interaction_task_unsupported")
	}
	var prompt string
	var references []webMedia
	hasVideoSource := false
	if json.Unmarshal(request.Input, &prompt) != nil {
		var parts []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			Data     string          `json:"data"`
			MIMEType string          `json:"mime_type"`
			URI      string          `json:"uri"`
			Content  json.RawMessage `json:"content,omitempty"`
		}
		if strictJSON(request.Input, &parts) != nil || len(parts) == 0 {
			return request, nil, failure(400, "interaction_text_input_only")
		}
		// The official SDK wraps supplied media parts in one user_input step.
		// This is still one new turn, not caller-supplied conversation history.
		if len(parts) == 1 && parts[0].Type == "user_input" {
			content := parts[0].Content
			if strictJSON(content, &parts) != nil || len(parts) == 0 {
				return request, nil, failure(400, "interaction_text_input_only")
			}
		}
		texts := make([]string, 0, len(parts))
		for _, part := range parts {
			switch part.Type {
			case "text":
				texts = append(texts, part.Text)
			case "image", "video":
				hasVideoSource = hasVideoSource || part.Type == "video"
				// A uri names either a Drive file, which is fetched, or a Files
				// entry, which has no API on this path to resolve it against.
				if part.URI != "" {
					_, drive := driveFileID(part.URI)
					_, stored := filesReferenceID(part.URI)
					if !drive && !stored {
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
	if format.Resolution != "" && (hasVideoSource || request.Previous != "") {
		return request, nil, failure(400, "interaction_resolution_inherited")
	}
	// Extension needs a video to continue. This surface can name two: the
	// interaction a previous turn stored, and a clip the caller uploaded with
	// this one. Neither present means there is nothing to extend.
	if request.GenerationConfig.VideoConfig.Task == interactionExtendTask && request.Previous == "" && !hasVideoSource {
		return request, nil, failure(400, "interaction_extend_requires_source")
	}
	if request.GenerationConfig.VideoConfig.Task == interactionExtendTask && request.Previous == "" && !webRoleDeclared(prompt) {
		prompt = "[# Sources <VIDEO_0>@Video1] " + prompt
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
	if body.ResponseFormat.Delivery == "uri" {
		if err := service.filesRequireWrite(ctx); err != nil {
			return nil, err
		}
	}
	native := request
	native.interactionDelivery = body.ResponseFormat.Delivery
	native.Model, native.Format, native.SourceFormat = omniModel, "gemini", "gemini"
	native.Stream = false
	native.freshContinuation = true
	previous := body.Previous
	if previous != "" && body.GenerationConfig.VideoConfig.Task == interactionExtendTask {
		if payload, err = withPreviousVideoDeclaration(payload); err != nil {
			return nil, err
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
	if body.Background {
		if err := service.markBackgroundInteraction(ctx, native, token); err != nil {
			return nil, err
		}
	}
	if request.Stream {
		return service.startInteractionStream(ctx, request, native, token, body.Store, body.Background)
	}
	if body.Background {
		if _, err := service.ownInteraction(ctx, native, token, body.Store, true); err != nil {
			return nil, err
		}
		return renderInteraction(request.AuthID, continuationResult{}, continuationView{Token: token, State: "pending"})
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
	// The submit can hold the stream for the whole budget, and a turn that comes
	// back pending is the one recovery exists for. Measuring the wait from before
	// the submit spends it on the submit itself, so the first deadline check ends
	// the turn without a single observation; generateVideo already measures its
	// budget from the submit that ended, and this path measures it the same way.
	var deadline time.Time
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
		if deadline.IsZero() {
			deadline = service.now().Add(webVideoBudget)
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
	rendered, err := renderInteraction(record.ID, result, receipt.View)
	if err != nil {
		return nil, err
	}
	return service.deliverInteraction(ctx, native, token, rendered.(continuationResult))
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
		message := view.ErrorMessage
		if message == "" {
			message = view.Error
		}
		body["error"] = map[string]string{"code": view.Error, "message": message}
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
