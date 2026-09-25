package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

var nextImageCredential atomic.Uint64

type imagesRequest struct {
	Prompt       string `json:"prompt"`
	N            int    `json:"n"`
	Size         string `json:"size"`
	Quality      string `json:"quality"`
	OutputFormat string `json:"output_format"`
	Background   string `json:"background"`
}

func routeImages(raw []byte) (interface{}, error) {
	var request modelRouteRequest
	if json.Unmarshal(raw, &request) != nil {
		return nil, failure(400, "invalid_route_request")
	}
	if claimsWebChatModel(request.RequestedModel) {
		return modelRouteResponse{Handled: true, TargetKind: "self", Reason: "chatgpt_web_chat"}, nil
	}
	if !claimsWebImageModel(request.RequestedModel) {
		return modelRouteResponse{Handled: false}, nil
	}
	return modelRouteResponse{Handled: true, TargetKind: "self", Reason: "chatgpt_web_image_generation"}, nil
}

func imagesModelBase(model string) string {
	base := strings.ToLower(strings.TrimSpace(model))
	if index := strings.LastIndex(base, "/"); index >= 0 && index < len(base)-1 {
		base = strings.TrimSpace(base[index+1:])
	}
	return base
}

func (service *service) executeImages(ctx context.Context, raw []byte) (interface{}, error) {
	var request executorRequest
	if json.Unmarshal(raw, &request) != nil {
		return nil, failure(400, "invalid_execution_request")
	}
	if !claimsWebImageModel(request.Model) {
		return nil, failure(400, "unsupported_model")
	}
	payload := request.Payload
	if len(payload) == 0 {
		payload = request.OriginalRequest
	}
	var images imagesRequest
	if json.Unmarshal(payload, &images) != nil {
		return nil, failure(400, "invalid_images_request")
	}
	images.Prompt = strings.TrimSpace(images.Prompt)
	if images.Prompt == "" {
		return nil, failure(400, "prompt_required")
	}
	if images.N > 1 {
		return nil, failure(400, "single_image_per_request")
	}
	body, err := service.generateWebImage(ctx, request.HostCallbackID, images)
	if err != nil {
		return nil, err
	}
	return executorResponse{Payload: body, Headers: http.Header{"Content-Type": []string{"application/json"}}}, nil
}

// imageCandidates keeps this path off the Codex API quota state. The host disables
// a ChatGPT credential once its Codex API allowance is spent, but the web
// image_gen allowance is a separate bucket that is still spendable, so disabled
// credentials are held in reserve instead of being dropped. Active ones are still
// preferred so that disabling a single credential keeps its intended effect.
func (service *service) imageCandidates(callbackID string) ([]hostEntry, error) {
	entries, err := service.entries(callbackID)
	if err != nil {
		return nil, err
	}
	active := make([]hostEntry, 0, len(entries))
	reserve := make([]hostEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Provider != authProvider {
			continue
		}
		if entry.Disabled {
			reserve = append(reserve, entry)
			continue
		}
		active = append(active, entry)
	}
	if len(active) > 0 {
		return active, nil
	}
	if len(reserve) > 0 {
		return reserve, nil
	}
	return nil, failure(503, "no_chatgpt_credential_available")
}

func (service *service) tokenFor(callbackID string, entry hostEntry) (string, error) {
	var stored struct {
		JSON json.RawMessage `json:"json"`
	}
	if err := service.callback("host.auth.get", callbackRequest{HostCallbackID: callbackID, AuthIndex: entry.AuthIndex}, &stored); err != nil {
		return "", err
	}
	var record struct {
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(stored.JSON, &record) != nil || record.AccessToken == "" {
		return "", failure(409, "credential_access_token_missing")
	}
	return record.AccessToken, nil
}

// imagesPayload renders the shape the host converts into the images API response.
func imagesPayload(result string, request imagesRequest) []byte {
	item := map[string]interface{}{
		"b64_json":      result,
		"output_format": valueOrDefault(request.OutputFormat, imagesDefaultFormat),
	}
	body := map[string]interface{}{
		"created": time.Now().Unix(),
		"data":    []interface{}{item},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return []byte(`{"created":0,"data":[]}`)
	}
	return raw
}

func valueOrDefault(value, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}
