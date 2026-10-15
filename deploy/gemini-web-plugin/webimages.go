package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// webImagesFormat is the format the host sends the OpenAI images endpoints in.
// It sends them only to models of type openai-image, which a generateContent
// model cannot also be, so each image model has a twin under this suffix.
const (
	webImagesFormat      = "openai-image"
	webImagesModelSuffix = "-image"
	webImagesBodyLimit   = 32 << 20
)

func webImagesRouteModel(id string) (webImageModel, bool) {
	base, ok := strings.CutSuffix(id, webImagesModelSuffix)
	if !ok {
		return webImageModel{}, false
	}
	return webImageModelFor(base)
}

func webImagesRouteModelInfos(account accountModels) []modelInfo {
	models := []modelInfo{}
	for _, image := range webImageModels {
		if _, ok := webFamilyCapability(account.Models, image.Family); ok {
			id := image.ID + webImagesModelSuffix
			models = append(models, modelInfo{ID: id, Object: "model", OwnedBy: provider, Type: webImagesFormat, Name: id, DisplayName: image.DisplayName + " Images", Description: "Image generation and editing through the Gemini web app in its " + image.Family + " mode, on /v1/images/generations and /v1/images/edits"})
		}
	}
	return models
}

// webImagesRequest reads the prompt and, for an edit, the images to work from.
// A generation is JSON; an edit is the multipart form the host forwards as it
// came, which names its boundary in its first line.
func webImagesRequest(payload []byte) (string, []webMedia, error) {
	if len(payload) > webImagesBodyLimit {
		return "", nil, failure(413, "images_request_too_large")
	}
	prompt, count := "", ""
	var media []webMedia
	if json.Valid(payload) {
		var body struct {
			Prompt string          `json:"prompt"`
			N      json.RawMessage `json:"n"`
		}
		if json.Unmarshal(payload, &body) != nil {
			return "", nil, failure(400, "images_request_invalid")
		}
		prompt, count = body.Prompt, string(body.N)
	} else {
		first, _, _ := bytes.Cut(payload, []byte("\r\n"))
		boundary, ok := strings.CutPrefix(string(first), "--")
		if !ok || boundary == "" {
			return "", nil, failure(400, "images_request_invalid")
		}
		reader := multipart.NewReader(bytes.NewReader(payload), boundary)
		for {
			part, err := reader.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return "", nil, failure(400, "images_request_invalid")
			}
			content, err := io.ReadAll(part)
			if err != nil {
				return "", nil, failure(400, "images_request_invalid")
			}
			switch part.FormName() {
			case "prompt":
				prompt = string(content)
			case "n":
				count = strings.TrimSpace(string(content))
			case "image", "image[]":
				mimeType := part.Header.Get("Content-Type")
				if !strings.HasPrefix(mimeType, "image/") {
					mimeType = http.DetectContentType(content)
				}
				media = append(media, webMedia{MIMEType: mimeType, Data: base64.StdEncoding.EncodeToString(content)})
			case "mask":
				// The web app edits from the whole image and a prompt; it has no
				// mask to paint into.
				return "", nil, failure(400, "images_mask_unsupported")
			}
		}
	}
	if strings.TrimSpace(prompt) == "" {
		return "", nil, failure(400, "image_prompt_required")
	}
	if count != "" && count != "1" && count != "null" {
		return "", nil, failure(400, "images_n_unsupported")
	}
	return prompt, media, nil
}

func (service *service) executeImagesRoute(ctx context.Context, method string, request executorRequest) (interface{}, error) {
	image, ok := webImagesRouteModel(request.Model)
	if !ok {
		return nil, failure(400, "unsupported_model")
	}
	if request.Stream || method == "executor.execute_stream" {
		return nil, failure(400, "images_stream_unsupported")
	}
	prompt, media, err := webImagesRequest(request.Payload)
	if err != nil {
		return nil, err
	}
	parts := []any{map[string]string{"text": prompt}}
	for _, item := range media {
		parts = append(parts, map[string]any{"inlineData": map[string]string{"mimeType": item.MIMEType, "data": item.Data}})
	}
	native := request
	native.Model, native.Format, native.SourceFormat = image.ID, "gemini", "gemini"
	if native.Payload, err = json.Marshal(map[string]any{"contents": []any{map[string]any{"role": "user", "parts": parts}}}); err != nil {
		return nil, err
	}
	result, err := service.executeRequest(ctx, "executor.execute", native)
	if err != nil {
		return nil, err
	}
	made, ok := result.(struct {
		Payload []byte
		Headers http.Header
	})
	if !ok {
		return nil, failure(502, "web_response_invalid")
	}
	body, err := webImagesResponse(made.Payload, service.now())
	if err != nil {
		return nil, err
	}
	return struct {
		Payload []byte
		Headers http.Header
	}{body, http.Header{"Content-Type": {"application/json"}}}, nil
}

func webImagesResponse(native []byte, now time.Time) ([]byte, error) {
	var reply struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					InlineData *struct {
						MIMEType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if json.Unmarshal(native, &reply) != nil || len(reply.Candidates) == 0 {
		return nil, failure(502, "web_response_invalid")
	}
	data := []any{}
	for _, part := range reply.Candidates[0].Content.Parts {
		if part.InlineData != nil && part.InlineData.Data != "" {
			data = append(data, map[string]string{"b64_json": part.InlineData.Data, "mime_type": part.InlineData.MIMEType})
		}
	}
	if len(data) == 0 {
		return nil, failure(502, "web_response_invalid")
	}
	return json.Marshal(map[string]any{"created": now.Unix(), "data": data})
}
