package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// responsesEndpoint serves image generation for a ChatGPT account with a plain
// bearer token; it is a variable so tests can point it at a local server.
var responsesEndpoint = "https://chatgpt.com/backend-api/codex/responses"

var nextImageCredential atomic.Uint64

type imagesRequest struct {
	Prompt       string `json:"prompt"`
	N            int    `json:"n"`
	Size         string `json:"size"`
	Quality      string `json:"quality"`
	OutputFormat string `json:"output_format"`
	Background   string `json:"background"`
}

func imagesModels() []modelInfo {
	return []modelInfo{{
		ID:                        imagesModel,
		Object:                    "model",
		OwnedBy:                   provider,
		Type:                      imagesModelType,
		DisplayName:               "ChatGPT Web Image",
		Name:                      imagesModel,
		Description:               "Image generation on the ChatGPT web image_gen allowance, independent of the Codex API limit",
		SupportedInputModalities:  []string{"text"},
		SupportedOutputModalities: []string{"image"},
	}}
}

func routeImages(raw []byte) (interface{}, error) {
	var request modelRouteRequest
	if json.Unmarshal(raw, &request) != nil {
		return nil, failure(400, "invalid_route_request")
	}
	if !claimsImageModel(request.RequestedModel) && !claimsWebImageModel(request.RequestedModel) {
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

func claimsImageModel(model string) bool {
	base := imagesModelBase(model)
	return base == imagesModel || strings.HasPrefix(base, "gpt-image-")
}

// imagesToolModelFor passes a client-requested gpt-image id straight through, which
// the web backend accepts, and falls back to the proven default for this plugin's
// own model id.
func imagesToolModelFor(model string) string {
	base := imagesModelBase(model)
	if strings.HasPrefix(base, "gpt-image-") {
		return base
	}
	return imagesToolModel
}

func (service *service) executeImages(ctx context.Context, raw []byte) (interface{}, error) {
	var request executorRequest
	if json.Unmarshal(raw, &request) != nil {
		return nil, failure(400, "invalid_execution_request")
	}
	if !claimsImageModel(request.Model) && !claimsWebImageModel(request.Model) {
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
	var body []byte
	var err error
	if claimsWebImageModel(request.Model) {
		body, err = service.generateWebImage(ctx, request.HostCallbackID, images)
	} else {
		body, err = service.generateImage(ctx, request.HostCallbackID, images, imagesToolModelFor(request.Model))
	}
	if err != nil {
		return nil, err
	}
	return executorResponse{Payload: body, Headers: http.Header{"Content-Type": []string{"application/json"}}}, nil
}

// generateImage walks the enabled ChatGPT credentials until one produces an image,
// so an account whose image allowance is spent hands the request to the next one.
func (service *service) generateImage(ctx context.Context, callbackID string, request imagesRequest, toolModel string) ([]byte, error) {
	if callbackID == "" {
		return nil, failure(401, "authenticated_execution_callback_required")
	}
	candidates, err := service.imageCandidates(callbackID)
	if err != nil {
		return nil, err
	}
	upstream, err := imagesUpstreamBody(request, toolModel)
	if err != nil {
		return nil, err
	}
	start := int(nextImageCredential.Add(1)-1) % len(candidates)
	var lastErr error = failure(503, "image_generation_unavailable")
	for offset := range candidates {
		entry := candidates[(start+offset)%len(candidates)]
		token, tokenErr := service.tokenFor(callbackID, entry)
		if tokenErr != nil {
			lastErr = tokenErr
			continue
		}
		result, generateErr := service.generateWith(ctx, token, upstream)
		if generateErr != nil {
			lastErr = generateErr
			continue
		}
		return imagesPayload(result, request), nil
	}
	return nil, lastErr
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

// generateWith runs one generation. The bearer token alone authenticates this
// endpoint, so it deliberately carries no browser fingerprint headers.
func (service *service) generateWith(ctx context.Context, token string, upstream []byte) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, responsesEndpoint, bytes.NewReader(upstream))
	if err != nil {
		return "", failure(500, "image_request_invalid")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := service.client.Do(request)
	if err != nil {
		return "", failure(502, "image_transport_failed")
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return "", failure(response.StatusCode, "image_credential_rejected")
	}
	return readImageResult(response.Body)
}

func imagesUpstreamBody(request imagesRequest, toolModel string) ([]byte, error) {
	tool := map[string]interface{}{
		"type":          "image_generation",
		"model":         valueOrDefault(toolModel, imagesToolModel),
		"action":        "generate",
		"size":          valueOrDefault(request.Size, imagesDefaultSize),
		"quality":       valueOrDefault(request.Quality, imagesDefaultQuality),
		"output_format": valueOrDefault(request.OutputFormat, imagesDefaultFormat),
	}
	if background := strings.TrimSpace(request.Background); background != "" {
		tool["background"] = background
	}
	body := map[string]interface{}{
		"model":        responsesModel,
		"instructions": imagesInstruction,
		"store":        false,
		"input": []interface{}{map[string]interface{}{
			"role":    "user",
			"content": []interface{}{map[string]interface{}{"type": "input_text", "text": request.Prompt}},
		}},
		"tools":       []interface{}{tool},
		"tool_choice": map[string]interface{}{"type": "image_generation"},
		"stream":      true,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, failure(500, "image_request_encoding_failed")
	}
	return raw, nil
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

// readImageResult scans the Responses SSE stream for the base64 image the
// image_generation tool emits.
func readImageResult(body io.Reader) (string, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), imagesMaxEventBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var event interface{}
		if json.Unmarshal([]byte(payload), &event) != nil {
			continue
		}
		if result := findImageResult(event); result != "" {
			return result, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", failure(502, "image_stream_read_failed")
	}
	return "", failure(502, "image_result_missing")
}

// findImageResult walks a decoded SSE event for the tool's base64 result field.
func findImageResult(node interface{}) string {
	switch value := node.(type) {
	case map[string]interface{}:
		if result, ok := value["result"].(string); ok && len(result) >= imagesMinResultBytes {
			return result
		}
		for _, child := range value {
			if found := findImageResult(child); found != "" {
				return found
			}
		}
	case []interface{}:
		for _, child := range value {
			if found := findImageResult(child); found != "" {
				return found
			}
		}
	}
	return ""
}

func valueOrDefault(value, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}
