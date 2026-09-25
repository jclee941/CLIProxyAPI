package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

// Going native removes the sidecar from the text path: the plugin speaks to the
// web product itself. The published model identifier is derived from the
// capability the account advertises rather than matched against a name, because
// once the sidecar is out of the path nothing else constrains which capability
// may be used.

// webPublicModelID renders the identifier callers use from the capability name
// the account advertises: "3.8 Flash" becomes gemini-web-flash-3.8.
func webPublicModelID(displayName string) (string, bool) {
	fields := strings.Fields(displayName)
	if len(fields) < 2 {
		return "", false
	}
	family := strings.ToLower(strings.Join(fields[1:], "-"))
	return provider + "-" + family + "-" + fields[0], true
}

func webSelectCapability(account webAccount, requested string) (capability, bool) {
	for _, entry := range account.Capabilities {
		if identifier, ok := webPublicModelID(entry.DisplayName); ok && identifier == requested {
			return entry, true
		}
	}
	return capability{}, false
}

// nativeText runs one text turn directly against the web product and renders the
// Gemini-native response the host expects, including any function calls the reply
// carried.
func (service *service) nativeText(ctx context.Context, record storageRecord, token sessionToken, requested string, payload []byte) ([]byte, error) {
	credential, err := decodeWebCredential(token)
	if err != nil {
		return nil, err
	}
	prompt, media, err := webContentsToPrompt(payload)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, failure(400, "text_only_flash_request")
	}
	session := service.newSession(credential)
	service.trackJar(record.TokenRef, session)
	defer service.persistJar(record.TokenRef, session)
	account, err := session.webCapabilities(ctx)
	if err != nil {
		return nil, err
	}
	model, ok := webSelectCapability(account, requested)
	if !ok {
		return nil, failure(404, "account_model_unavailable")
	}
	sources, err := service.mediaSources(ctx, media, "")
	if err != nil {
		return nil, err
	}
	attachments, err := session.uploadSources(ctx, sources)
	if err != nil {
		return nil, err
	}
	text, err := session.generateText(ctx, prompt, account, model, attachments)
	if err != nil {
		return nil, err
	}
	return webRenderResponse(requested, text, payload)
}

// nativeVideo produces the same response body the sidecar would, so the caller's
// submission bookkeeping and validation stay untouched when only the transport
// changes.
func (service *service) nativeVideo(ctx context.Context, record storageRecord, token sessionToken, payload []byte) (httpResponse, error) {
	credential, err := decodeWebCredential(token)
	if err != nil {
		return httpResponse{}, err
	}
	base, options, err := omniRequest(payload)
	if err != nil {
		return httpResponse{}, err
	}
	_, media, err := webContentsToPrompt(payload)
	if err != nil {
		return httpResponse{}, err
	}
	prompt := options.applyPrompt(base)
	session := service.newSession(credential)
	service.trackJar(record.TokenRef, session)
	defer service.persistJar(record.TokenRef, session)
	account, err := session.webCapabilities(ctx)
	if err != nil {
		return httpResponse{}, err
	}
	model, ok := webVideoCapability(account)
	if !ok {
		return httpResponse{}, failure(404, "account_model_unavailable")
	}
	sources, err := service.mediaSources(ctx, media, "")
	if err != nil {
		return httpResponse{}, err
	}
	attachments, err := session.uploadSources(ctx, sources)
	if err != nil {
		return httpResponse{}, err
	}
	video, err := session.generateVideo(ctx, prompt, account, model, options.framing(), attachments)
	if err != nil {
		service.noteVideoRefusal(record.ID, err)
		return httpResponse{}, err
	}
	service.noteVideoDelivered(record.ID)
	body, err := json.Marshal(map[string]any{
		"modelVersion": omniModel,
		"candidates": []any{map[string]any{
			"index":        0,
			"finishReason": "STOP",
			"content": map[string]any{"role": "model", "parts": []any{
				map[string]any{"inlineData": map[string]any{"mimeType": "video/mp4", "data": base64.StdEncoding.EncodeToString(video)}},
			}},
		}},
	})
	if err != nil {
		return httpResponse{}, failure(500, "web_response_invalid")
	}
	return httpResponse{StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}}, Body: body}, nil
}

// webVideoCapability picks the capability the video turn runs on, matching the
// bridge it replaces: the first the account offers in its standard mode.
func webVideoCapability(account webAccount) (capability, bool) {
	for _, entry := range account.Capabilities {
		if entry.Mode == 1 {
			return entry, true
		}
	}
	return capability{}, false
}

// webExecutionResult matches the envelope the sidecar path returns, so the two
// are interchangeable to the host.
func webExecutionResult(body []byte, stream bool) interface{} {
	if stream {
		payload := append([]byte("data: "), body...)
		payload = append(payload, '\n', '\n')
		return struct {
			Headers http.Header                `json:"headers"`
			Chunks  []struct{ Payload []byte } `json:"chunks"`
		}{http.Header{"Content-Type": {"text/event-stream"}}, []struct{ Payload []byte }{{payload}}}
	}
	return struct {
		Payload []byte
		Headers http.Header
	}{body, http.Header{"Content-Type": {"application/json"}}}
}

func webRenderResponse(requested, text string, payload []byte) ([]byte, error) {
	var request map[string]any
	if json.Unmarshal(payload, &request) != nil {
		return nil, failure(400, "invalid_generation_request")
	}
	parts := make([]any, 0, 2)
	if len(webToolDefinitions(request)) > 0 {
		clean, calls := webParseFunctionCalls(text)
		if strings.TrimSpace(clean) != "" {
			parts = append(parts, map[string]any{"text": clean})
		}
		for _, call := range calls {
			parts = append(parts, map[string]any{"functionCall": map[string]any{"name": call.Name, "args": call.Args}})
		}
	}
	if len(parts) == 0 {
		parts = append(parts, map[string]any{"text": text})
	}
	response := map[string]any{
		"modelVersion": requested,
		"candidates": []any{map[string]any{
			"index":        0,
			"finishReason": "STOP",
			"content":      map[string]any{"role": "model", "parts": parts},
		}},
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, failure(500, "web_response_invalid")
	}
	return encoded, nil
}
