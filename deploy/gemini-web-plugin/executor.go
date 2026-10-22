package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"
)

func (service *service) execute(ctx context.Context, method string, raw []byte) (interface{}, error) {
	var request executorRequest
	if json.Unmarshal(raw, &request) != nil {
		return nil, failure(400, "invalid_execution_request")
	}
	if request.Format == webImagesFormat {
		return service.executeImagesRoute(ctx, method, request)
	}
	return service.executeRequest(ctx, method, request)
}

func (service *service) executeRequest(ctx context.Context, method string, request executorRequest) (interface{}, error) {
	if method == "executor.count_tokens" {
		return nil, failure(400, "count_tokens_not_supported")
	}
	if request.Format == "interactions" {
		request.Stream = request.Stream || method == "executor.execute_stream"
		service.noteExecutorAccount(request)
		result, err := service.executeInteraction(ctx, request)
		if err != nil {
			return nil, interactionAccountFailure(service.accountDisplay(request.AuthID), executionFailure(omniModel, err))
		}
		return result, nil
	}
	if request.Format != "gemini" || request.Alt != "" {
		return nil, failure(400, "unsupported_execution_format")
	}
	image, isImage := webImageModelFor(request.Model)
	isMusic := request.Model == webMusicModel
	if !webTextModel(request.Model) && request.Model != omniModel && !isImage && !isMusic {
		return nil, failure(400, "unsupported_model")
	}
	stream := request.Stream || method == "executor.execute_stream"
	if _, _, chained, err := service.tuning().continuationRequest(request.Payload); chained {
		if err != nil {
			return nil, executionFailure(omniModel, err)
		}
		request.Stream = stream
		result, err := service.executeContinuation(ctx, request)
		if err != nil {
			return nil, executionFailure(omniModel, err)
		}
		return result, nil
	}
	if request.Model == omniModel {
		if stream {
			return nil, failure(400, "omni_native_nonstreaming_only")
		}
		prompt, options, promptErr := omniRequestWithin(request.Payload, service.tuning().omniRequestBytes())
		if promptErr != nil {
			return nil, promptErr
		}
		if len(request.OriginalRequest) > 0 && !omniOriginalAccepted(request.OriginalRequest) {
			return nil, failure(400, "unsupported_omni_request")
		}
		// The upstream body is rebuilt from the validated prompt, so nothing a
		// caller or a front-end format translation added can reach the
		// generation call.
		payload, payloadErr := omniGeminiPayload(prompt, options)
		if payloadErr != nil {
			return nil, payloadErr
		}
		request.Payload = payload
		if !hasOmniStopRules(request.AuthMetadata.RequestScopedErrors) {
			return nil, failure(400, "omni_requires_host_request_stop_policy")
		}
	}
	if request.AuthProvider != provider || request.Metadata.PinnedAuthID != "" && request.Metadata.PinnedAuthID != request.AuthID {
		return nil, failure(400, "auth_identity_mismatch")
	}
	record, err := service.parseStorage(request.StorageJSON, false)
	if err != nil {
		return nil, executionFailure(request.Model, err)
	}
	if record.ID != request.AuthID {
		return nil, failure(400, "auth_identity_mismatch")
	}
	if record.Disabled {
		return nil, executionFailure(request.Model, failure(409, "account_disabled"))
	}
	exclusive := request.Model == omniModel
	if _, err := service.accountLease(record); err != nil {
		return nil, executionFailure(request.Model, err)
	}
	lease, err := service.acquireCredential(record.TokenRef, exclusive)
	if err != nil {
		return nil, executionFailure(request.Model, err)
	}
	if exclusive {
		defer lease.guard.Unlock()
	} else {
		defer lease.guard.RUnlock()
	}
	_, token, err := service.resolve(ctx, request.StorageJSON, request.AuthID)
	if err != nil {
		return nil, executionFailure(request.Model, err)
	}
	if request.Model == omniModel {
		if lease.snapshot().state == maintenanceHostPending {
			return nil, executionFailure(request.Model, failure(409, "host_sync_pending"))
		}
		token, err = service.renewLocalSession(ctx, request.HostCallbackID, record)
		if err != nil {
			return nil, executionFailure(request.Model, err)
		}
		if lease.snapshot().state == maintenanceHostPending && request.HostCallbackID != "" {
			if err := service.syncCredentialHost(request.HostCallbackID, record); err != nil {
				return nil, executionFailure(request.Model, err)
			}
			lease.set(credentialState{state: maintenanceReady})
		}
	}
	if isMusic {
		body, errNative := service.nativeMusic(ctx, record, token, request.Payload)
		if errNative != nil {
			return nil, executionFailure(request.Model, errNative)
		}
		return webExecutionResult(body, stream), nil
	}
	if isImage {
		body, errNative := service.nativeImage(ctx, record, token, image, request.Payload)
		if errNative != nil {
			return nil, executionFailure(request.Model, errNative)
		}
		return webExecutionResult(body, stream), nil
	}
	if webTextModel(request.Model) {
		body, errNative := service.nativeText(ctx, record, token, request.Model, request.Payload)
		if errNative != nil {
			return nil, executionFailure(request.Model, errNative)
		}
		return webExecutionResult(body, stream), nil
	}
	if err := service.localSubmission(record, localSubmitting); err != nil {
		return nil, executionFailure(request.Model, err)
	}
	response, err := service.nativeVideo(ctx, record, token, request.Payload)
	if err != nil {
		return nil, executionFailure(request.Model, err)
	}
	if err := validateVideoResponse(response.Body); err != nil {
		lease.set(credentialState{state: maintenanceOperator, errCode: "submission_outcome_unknown"})
		return nil, executionFailure(request.Model, err)
	}
	if err := service.localSubmission(record, localReady); err != nil {
		return nil, executionFailure(request.Model, err)
	}
	return struct {
		Payload []byte
		Headers http.Header
	}{response.Body, response.Headers}, nil
}

func executionFailure(model string, err error) error {
	if model != omniModel {
		var public *publicError
		if errors.As(err, &public) && public.Code == "needs_operator" {
			// Recovery fences this account, not the stateless request. A 409
			// is a caller fault to the host and prevents trying a ready account.
			unavailable := *public
			unavailable.HTTPStatus = http.StatusServiceUnavailable
			return &unavailable
		}
		return err
	}
	var public *publicError
	if errors.As(err, &public) {
		// The host matches its stop rules against the error text, which is the
		// message, so the prefix has to be there and not only in the code. Every
		// failure whose message is its code comes out exactly as before; one
		// carrying what the product said keeps it, after the prefix.
		return &publicError{
			Code:       "gemini_web_omni:" + public.Code,
			Message:    "gemini_web_omni:" + public.Message,
			HTTPStatus: public.HTTPStatus,
		}
	}
	return failure(502, "gemini_web_omni:submission_outcome_unknown")
}

func hasOmniStopRules(rules []stopRule) bool {
	for status := 300; status < 600; status++ {
		found := false
		for _, rule := range rules {
			if rule.Status != status {
				continue
			}
			for _, match := range rule.Match {
				if match == "gemini_web_omni:" && rule.Action == "stop" {
					found = true
				}
			}
			break
		}
		if !found {
			return false
		}
	}
	return true
}

// omniRequest validates a Gemini-native omni request and returns the single text
// prompt it carries together with the options the web path can honour. Fixed
// safety thresholds are accepted because the host's OpenAI-to-Gemini translation
// adds them; they are dropped rather than forwarded, since the upstream body is
// rebuilt from the prompt and those options alone.
// omniRequestLimit bounds a native Omni request body.
const omniRequestLimit = 5 * 1024 * 1024

func omniRequest(raw []byte) (string, omniOptions, error) {
	return omniRequestWithin(raw, omniRequestLimit)
}

// omniRequestWithin parses a native Omni request no larger than limit.
func omniRequestWithin(raw []byte, limit int) (string, omniOptions, error) {
	var body struct {
		Model    string `json:"model"`
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text            *string        `json:"text"`
				InlineData      *webInlinePart `json:"inlineData"`
				InlineDataSnake *webInlinePart `json:"inline_data"`
				FileData        *webFilePart   `json:"fileData"`
				FileDataSnake   *webFilePart   `json:"file_data"`
				// The OpenAI bridge hangs a thought signature off an image part,
				// and unknown fields are refused, so it has to be named here.
				ThoughtSignature string `json:"thoughtSignature"`
			} `json:"parts"`
		} `json:"contents"`
		GenerationConfig map[string]json.RawMessage `json:"generationConfig"`
		SafetySettings   json.RawMessage            `json:"safetySettings"`
	}
	if len(raw) > limit || strictJSON(raw, &body) != nil || body.Model != "" && body.Model != omniModel || len(body.Contents) != 1 {
		return "", omniOptions{}, failure(400, "unsupported_omni_request")
	}
	options, optionsErr := parseOmniOptions(body.GenerationConfig)
	if optionsErr != nil {
		return "", omniOptions{}, optionsErr
	}
	turn := body.Contents[0]
	if turn.Role != "" && turn.Role != "user" || len(turn.Parts) == 0 {
		return "", omniOptions{}, failure(400, "omni_single_user_turn_required")
	}
	texts := make([]string, 0, len(turn.Parts))
	for _, part := range turn.Parts {
		inline := part.InlineData
		if inline == nil {
			inline = part.InlineDataSnake
		}
		file := part.FileData
		if file == nil {
			file = part.FileDataSnake
		}
		switch {
		case part.Text != nil:
			texts = append(texts, *part.Text)
		case file != nil:
			_, drive := driveFileID(file.uri())
			_, stored := filesReferenceID(file.uri())
			_, chained := parseChainedReference(file.uri())
			if !drive && !stored && !chained {
				return "", omniOptions{}, failure(400, "attachment_reference_unsupported")
			}
		case inline != nil:
			// A reference image is uploaded and referenced beside the prompt
			// rather than carried inside it, so the bytes are only checked here.
			if inline.mimeType() == "" || inline.Data == "" {
				return "", omniOptions{}, failure(400, "omni_reference_invalid")
			}
		default:
			return "", omniOptions{}, failure(400, "omni_text_only")
		}
	}
	base := strings.Join(texts, "\n")
	if strings.TrimSpace(base) == "" {
		return "", omniOptions{}, failure(400, "omni_prompt_length_invalid")
	}
	if utf8.RuneCountInString(options.applyPrompt(base)) > 8000 {
		return "", omniOptions{}, failure(400, "omni_prompt_length_invalid")
	}
	return base, options, nil
}

func validateOmni(raw []byte) error {
	_, _, err := omniRequest(raw)
	return err
}

// omniOriginalAccepted reports whether the caller's untranslated request is a
// single-turn omni request in one of the two supported client formats, so that
// a request the caller did not intend can never burn a generation.
func omniOriginalAccepted(raw []byte) bool {
	if _, _, err := omniRequest(raw); err == nil {
		return true
	}
	_, err := openAIPromptForOmni(raw)
	return err == nil
}

func validateVideoResponse(raw []byte) error {
	var response struct {
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
	if json.Unmarshal(raw, &response) != nil || len(response.Candidates) != 1 || len(response.Candidates[0].Content.Parts) != 1 {
		return failure(502, "invalid_video_response")
	}
	video := response.Candidates[0].Content.Parts[0].InlineData
	if video.MIMEType != "video/mp4" || video.Data == "" {
		return failure(502, "invalid_video_response")
	}
	return nil
}
