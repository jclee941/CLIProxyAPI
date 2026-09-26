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
	if method == "executor.count_tokens" {
		return nil, failure(400, "count_tokens_not_supported")
	}
	if request.Format == "interactions" {
		request.Stream = request.Stream || method == "executor.execute_stream"
		result, err := service.executeInteraction(ctx, request)
		if err != nil {
			return nil, executionFailure(omniModel, err)
		}
		return result, nil
	}
	if request.Format != "gemini" || request.Alt != "" {
		return nil, failure(400, "unsupported_execution_format")
	}
	if request.Model != flashModel && request.Model != omniModel {
		return nil, failure(400, "unsupported_model")
	}
	stream := request.Stream || method == "executor.execute_stream"
	if _, _, chained, err := continuationRequest(request.Payload); chained {
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
		prompt, options, promptErr := omniRequest(request.Payload)
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
	if request.Model == flashModel && service.settings().NativeGeneration {
		body, errNative := service.nativeText(ctx, record, token, request.Model, request.Payload)
		if errNative != nil {
			return nil, executionFailure(request.Model, errNative)
		}
		return webExecutionResult(body, stream), nil
	}
	model := omniModel
	if request.Model == flashModel {
		model = "gemini-3.8-flash"
		account, err := service.accountModels(ctx, record.TokenRef, token)
		if err != nil {
			return nil, err
		}
		found := false
		for _, available := range verifiedModels(account) {
			if available.ID == flashModel {
				found = true
			}
		}
		if !found {
			return nil, failure(404, "account_model_unavailable")
		}
	}
	body, err := nativePayload(request.Payload, model)
	if err != nil {
		return nil, err
	}
	if exclusive {
		if err := service.localSubmission(record, localSubmitting); err != nil {
			return nil, executionFailure(request.Model, err)
		}
	}
	var response httpResponse
	if request.Model == omniModel && service.settings().NativeGeneration {
		response, err = service.nativeVideo(ctx, record, token, request.Payload)
	} else {
		response, err = service.sidecar(ctx, sidecarRequest{Method: "POST", Path: "/v1beta/models/" + model + ":generateContent", Token: token, Body: body, Reference: record.TokenRef})
	}
	if err != nil {
		if exclusive {
			switch safeCredentialCode(err) {
			case "sidecar_transport_failed", "sidecar_response_failed", "sidecar_response_invalid", "sidecar_caller_disconnected":
				lease.set(credentialState{state: maintenanceOperator, errCode: "submission_outcome_unknown"})
			}
		}
		return nil, executionFailure(request.Model, err)
	}
	if request.Model == omniModel {
		if err := validateVideoResponse(response.Body); err != nil {
			lease.set(credentialState{state: maintenanceOperator, errCode: "submission_outcome_unknown"})
			return nil, executionFailure(request.Model, err)
		}
		if err := service.localSubmission(record, localReady); err != nil {
			return nil, executionFailure(request.Model, err)
		}
	}
	if stream {
		payload := append([]byte("data: "), response.Body...)
		payload = append(payload, '\n', '\n')
		return struct {
			Headers http.Header                `json:"headers"`
			Chunks  []struct{ Payload []byte } `json:"chunks"`
		}{http.Header{"Content-Type": {"text/event-stream"}}, []struct{ Payload []byte }{{payload}}}, nil
	}
	return struct {
		Payload []byte
		Headers http.Header
	}{response.Body, response.Headers}, nil
}

func executionFailure(model string, err error) error {
	if model != omniModel {
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
func omniRequest(raw []byte) (string, omniOptions, error) {
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
	if len(raw) > 5*1024*1024 || strictJSON(raw, &body) != nil || body.Model != "" && body.Model != omniModel || len(body.Contents) != 1 {
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

func nativePayload(raw []byte, model string) ([]byte, error) {
	var body map[string]json.RawMessage
	if len(raw) > 5*1024*1024 || json.Unmarshal(raw, &body) != nil || body == nil {
		return nil, failure(400, "invalid_generation_request")
	}
	if _, exists := body["model"]; exists {
		encoded, err := json.Marshal(model)
		if err != nil {
			return nil, failure(500, "model_encoding_failed")
		}
		body["model"] = encoded
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, failure(400, "invalid_generation_request")
	}
	return encoded, nil
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

func (service *service) executorHTTP(ctx context.Context, raw []byte) (interface{}, error) {
	var request executorHTTPRequest
	if json.Unmarshal(raw, &request) != nil || request.Method != "GET" || request.URL != sidecarBase+"/v1/usage" && request.URL != sidecarBase+"/v1/account-models" {
		return nil, failure(400, "executor_http_route_denied")
	}
	if request.AuthProvider != provider {
		return nil, failure(400, "auth_identity_mismatch")
	}
	record, token, err := service.resolve(ctx, request.StorageJSON, request.AuthID)
	if err != nil {
		return nil, err
	}
	if request.URL == sidecarBase+"/v1/usage" {
		usage, err := service.usage(ctx, record.TokenRef, token)
		if err != nil {
			return nil, err
		}
		return managementJSON(200, usage)
	}
	account, err := service.accountModels(ctx, record.TokenRef, token)
	if err != nil {
		return nil, err
	}
	return managementJSON(200, account)
}
