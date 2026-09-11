package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

type sidecarRequest struct {
	Method, Path string
	Token        sessionToken
	Body         []byte
}

func newSidecarClient() *http.Client {
	return &http.Client{
		Transport:     &http.Transport{Proxy: nil, MaxIdleConns: 10, MaxIdleConnsPerHost: 10, DisableCompression: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (service *service) sidecar(ctx context.Context, request sidecarRequest) (httpResponse, error) {
	allowed := request.Method == "GET" && (request.Path == "/v1/account-models" || request.Path == "/v1/usage")
	allowed = allowed || request.Method == "POST" && (request.Path == "/v1beta/models/gemini-3.8-flash:generateContent" || request.Path == "/v1beta/models/gemini-web-omni:generateContent")
	allowed = allowed || request.Method == "POST" && request.Path == "/v1/session/renew" && len(request.Body) == 0
	if !allowed {
		return httpResponse{}, failure(400, "sidecar_route_denied")
	}
	upstream, err := http.NewRequestWithContext(ctx, request.Method, sidecarBase+request.Path, bytes.NewReader(request.Body))
	if err != nil {
		return httpResponse{}, failure(400, "sidecar_request_invalid")
	}
	upstream.Header.Set("x-goog-api-key", request.Token.value)
	upstream.Header.Set("Content-Type", "application/json")
	response, err := service.client.Do(upstream)
	if err != nil {
		return httpResponse{}, failure(502, "sidecar_transport_failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 128*1024*1024+1))
	if err != nil || len(body) > 128*1024*1024 {
		return httpResponse{}, failure(502, "sidecar_response_failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code := "sidecar_request_failed"
		if response.StatusCode >= 500 && response.StatusCode < 600 {
			var upstreamError struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(body, &upstreamError) == nil {
				switch upstreamError.Error.Message {
				case "missing_video_operation", "video_operation_mismatch", "invalid_upstream_frame",
					"missing_upstream_response", "no_video_generated", "invalid_video_download",
					"invalid_video_origin", "video_download_failed", "invalid_response", "bootstrap_failed",
					"unauthenticated", "rpc_denied", "model_unavailable", "caller_disconnected":
					code = "sidecar_" + upstreamError.Error.Message
				}
			}
		}
		return httpResponse{}, failure(response.StatusCode, code)
	}
	if !json.Valid(body) {
		return httpResponse{}, failure(502, "sidecar_response_invalid")
	}
	return httpResponse{StatusCode: response.StatusCode, Headers: http.Header{"Content-Type": {"application/json"}}, Body: body}, nil
}

type capability struct {
	CapabilityID string `json:"capability_id"`
	DisplayName  string `json:"display_name"`
	Mode         int    `json:"mode"`
}
type accountModels struct {
	Available  bool         `json:"available"`
	StatusCode *int         `json:"status_code"`
	Models     []capability `json:"models"`
	ObservedAt float64      `json:"observed_at"`
}

func (service *service) accountModels(ctx context.Context, token sessionToken) (accountModels, error) {
	response, err := service.sidecar(ctx, sidecarRequest{Method: "GET", Path: "/v1/account-models", Token: token})
	if err != nil {
		return accountModels{}, err
	}
	var account accountModels
	if err := json.Unmarshal(response.Body, &account); err != nil {
		return account, failure(502, "account_response_invalid")
	}
	if !account.Available {
		return account, failure(401, "account_unavailable")
	}
	return account, nil
}

func verifiedModels(account accountModels) []modelInfo {
	models := make([]modelInfo, 0, 2)
	if !account.Available {
		return models
	}
	matches := 0
	for _, capability := range account.Models {
		if capability.DisplayName == "3.8 Flash" && capability.CapabilityID != "" {
			matches++
		}
	}
	if matches == 1 {
		models = append(models, modelInfo{ID: flashModel, Object: "model", OwnedBy: provider, Type: provider, Name: flashModel, DisplayName: "Gemini Web Flash 3.8", Description: "Account-verified 3.8 Flash; text and tool emulation; buffered streaming", SupportedGenerationMethods: []string{"generateContent", "streamGenerateContent"}, SupportedInputModalities: []string{"text"}, SupportedOutputModalities: []string{"text"}})
	}
	models = append(models, modelInfo{ID: omniModel, Object: "model", OwnedBy: provider, Type: provider, Name: omniModel, DisplayName: "Gemini Web Omni", Description: "Web video tool; synchronous Gemini-native single text turn only; availability determined at execution", SupportedGenerationMethods: []string{"generateContent"}, SupportedInputModalities: []string{"text"}, SupportedOutputModalities: []string{"video"}})
	return models
}
