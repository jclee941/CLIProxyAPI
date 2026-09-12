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
	if request.Format != "gemini" || request.Alt != "" {
		return nil, failure(400, "unsupported_execution_format")
	}
	if request.Model != flashModel && request.Model != omniModel {
		return nil, failure(400, "unsupported_model")
	}
	stream := request.Stream || method == "executor.execute_stream"
	if request.Model == omniModel {
		if stream || request.SourceFormat != "gemini" {
			return nil, failure(400, "omni_native_nonstreaming_only")
		}
		if err := validateOmni(request.Payload); err != nil {
			return nil, err
		}
		if len(request.OriginalRequest) > 0 {
			if err := validateOmni(request.OriginalRequest); err != nil {
				return nil, err
			}
		}
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
	lease, err := service.acquireCredential(record.TokenRef, exclusive)
	if err != nil {
		return nil, executionFailure(request.Model, err)
	}
	if exclusive {
		defer lease.guard.Unlock()
	} else {
		defer lease.guard.RUnlock()
	}
	if binding, bound := service.settings().MaintenanceSources[record.ID]; bound && binding.TokenRef != record.TokenRef {
		return nil, executionFailure(request.Model, failure(409, "binding_mismatch"))
	}
	_, token, err := service.resolve(ctx, request.StorageJSON, request.AuthID)
	if err != nil {
		return nil, executionFailure(request.Model, err)
	}
	if request.Model == omniModel {
		if lease.snapshot().state == maintenanceHostPending {
			return nil, executionFailure(request.Model, failure(409, "host_sync_pending"))
		}
		token, err = service.renewForVideo(ctx, record, token)
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
	response, err := service.sidecar(ctx, sidecarRequest{Method: "POST", Path: "/v1beta/models/" + model + ":generateContent", Token: token, Body: body, Reference: record.TokenRef})
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
		return failure(public.HTTPStatus, "gemini_web_omni:"+public.Code)
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

func validateOmni(raw []byte) error {
	var body struct {
		Model    string `json:"model"`
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text *string `json:"text"`
			} `json:"parts"`
		} `json:"contents"`
		GenerationConfig map[string]json.RawMessage `json:"generationConfig"`
	}
	if len(raw) > 5*1024*1024 || strictJSON(raw, &body) != nil || body.Model != "" && body.Model != omniModel || len(body.GenerationConfig) != 0 || len(body.Contents) != 1 {
		return failure(400, "unsupported_omni_request")
	}
	turn := body.Contents[0]
	if turn.Role != "" && turn.Role != "user" || len(turn.Parts) == 0 {
		return failure(400, "omni_single_user_turn_required")
	}
	texts := make([]string, 0, len(turn.Parts))
	for _, part := range turn.Parts {
		if part.Text == nil {
			return failure(400, "omni_text_only")
		}
		texts = append(texts, *part.Text)
	}
	prompt := strings.Join(texts, "\n")
	if strings.TrimSpace(prompt) == "" || utf8.RuneCountInString(prompt) > 8000 {
		return failure(400, "omni_prompt_length_invalid")
	}
	return nil
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
