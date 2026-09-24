package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
)

// URI delivery changes only the creation/SSE representation. The existing
// durable interaction result remains inline for the documented GET behavior.
func (service *service) deliverInteraction(ctx context.Context, request executorRequest, token string, result continuationResult) (continuationResult, error) {
	if request.interactionDelivery != "uri" {
		return result, nil
	}
	var body struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Account string `json:"account"`
		Status  string `json:"status"`
		Steps   []struct {
			Type    string `json:"type"`
			Content []struct {
				Type     string `json:"type"`
				MIMEType string `json:"mime_type"`
				Data     string `json:"data,omitempty"`
				URI      string `json:"uri,omitempty"`
			} `json:"content"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(result.Payload, &body); err != nil {
		return continuationResult{}, err
	}
	if body.Status != "completed" {
		return result, nil
	}
	video := &body.Steps[0].Content[0]
	content, err := base64.StdEncoding.Strict().DecodeString(video.Data)
	if err != nil {
		return continuationResult{}, failure(502, "invalid_interaction_video")
	}
	file, err := service.saveGeneratedFile(ctx, request.Metadata.CallerScope, token+":video:0", video.MIMEType, content)
	if err != nil {
		return continuationResult{}, err
	}
	video.URI, video.Data = file.URI, ""
	result.Payload, err = json.Marshal(body)
	return result, err
}
