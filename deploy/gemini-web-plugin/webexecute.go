package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

// The plugin speaks to the web product itself. The published model identifier is
// derived from the capability the account advertises rather than matched against
// a name, because nothing else constrains which capability may be used.

// webTextModelPrefix starts every text model identifier; the account's mode
// name follows it.
const webTextModelPrefix = "gemini-app-"

// webPublicModelID renders the identifier callers use from the capability name
// the account advertises: "3.8 Flash" becomes gemini-app-3.8-flash.
func webPublicModelID(displayName string) (string, bool) {
	fields := strings.Fields(displayName)
	if len(fields) < 2 {
		return "", false
	}
	return webTextModelPrefix + strings.ToLower(strings.Join(fields, "-")), true
}

// webTextModel reports whether a model is one of the text models: an account
// mode, or the internal 3.8 Flash name the continuation protocol uses.
func webTextModel(model string) bool {
	return model == flashModel || strings.HasPrefix(model, webTextModelPrefix)
}

func webSelectCapability(account webAccount, requested string) (capability, bool) {
	for _, entry := range account.Capabilities {
		identifier, ok := webPublicModelID(entry.DisplayName)
		if ok && identifier == requested || requested == flashModel && entry.DisplayName == "3.8 Flash" {
			return entry, true
		}
	}
	return capability{}, false
}

var webTextThinkingLevels = []string{"low", "medium", "high"}

// webTextModelInfos publishes a text model for every mode the account has.
func webTextModelInfos(account accountModels) []modelInfo {
	models := []modelInfo{}
	seen := map[string]bool{}
	for _, entry := range account.Models {
		id, ok := webPublicModelID(entry.DisplayName)
		if !ok || seen[id] {
			continue
		}
		// 3.1 Pro answered every text turn on every account with a generic error
		// or a stream that never came (2026-10-15); the operator runs on Argon.
		if fields := strings.Fields(entry.DisplayName); len(fields) > 1 && strings.Join(fields[1:], " ") == "Pro" {
			continue
		}
		seen[id] = true
		models = append(models, modelInfo{ID: id, Object: "model", OwnedBy: provider, Type: provider, Name: id, DisplayName: "Gemini App " + entry.DisplayName, Description: "Text turns through the Gemini web app in its " + entry.DisplayName + " mode", SupportedGenerationMethods: []string{"generateContent", "streamGenerateContent"}, SupportedInputModalities: []string{"text", "image"}, SupportedOutputModalities: []string{"text"}, Thinking: &thinkingSupport{Levels: webTextThinkingLevels}})
	}
	return models
}

// webThinkingLevels maps a Gemini thinkingConfig.thinkingLevel to the web app's
// thinking level, protocol field 81: standard, medium and extended.
var webThinkingLevels = map[string]int{"low": 1, "medium": 4, "high": 2}

func webThinkingLevel(payload []byte) (int, error) {
	var request struct {
		GenerationConfig struct {
			ThinkingConfig struct {
				ThinkingLevel string `json:"thinkingLevel"`
			} `json:"thinkingConfig"`
		} `json:"generationConfig"`
	}
	if json.Unmarshal(payload, &request) != nil {
		return 0, failure(400, "invalid_generation_request")
	}
	name := strings.ToLower(strings.TrimSpace(request.GenerationConfig.ThinkingConfig.ThinkingLevel))
	if name == "" {
		return 0, nil
	}
	level, ok := webThinkingLevels[name]
	if !ok {
		return 0, failure(400, "invalid_thinking_level")
	}
	return level, nil
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
	level, err := webThinkingLevel(payload)
	if err != nil {
		return nil, err
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
	raw, err := session.sendTurn(ctx, prompt, account, model, attachments, level)
	if err != nil {
		return nil, err
	}
	if service.settings().DeleteChats {
		defer service.deleteChat(ctx, session, webReplyConversation(raw))
	}
	text, err := webReplyText(raw)
	if err != nil {
		return nil, err
	}
	return webRenderResponse(requested, text, payload)
}

// nativeVideo produces the Gemini-native video response body the caller's
// submission bookkeeping and validation expect.
func (service *service) nativeVideo(ctx context.Context, record storageRecord, token sessionToken, payload []byte) (result httpResponse, err error) {
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
	model, err := webVideoCapabilityFor(account, options.VideoMode, service.settings().VideoMode)
	if err != nil {
		return httpResponse{}, err
	}
	session.diag = newVideoTurnDiag(service.recordAccountName(record), "first", account, model, options.framing(), options.thinking(service.settings().VideoThinkingLevel), options.language())
	session.videoWire = service.settings().VideoWireMode
	defer func() { service.reportVideoTurn(session.diag, videoOutcome(err)) }()
	sources, err := service.mediaSources(ctx, media, "")
	if err != nil {
		return httpResponse{}, err
	}
	attachments, err := session.uploadSources(ctx, sources)
	if err != nil {
		return httpResponse{}, err
	}
	video, err := session.generateVideo(ctx, prompt, account, model, options.framing(), options.thinking(service.settings().VideoThinkingLevel), options.language(), attachments)
	if standard, again := standardModeFallback(account, err); again && proseFallbackEligible(options.VideoMode, model, session.diag) {
		service.reportVideoTurn(session.diag, videoOutcome(err))
		session.diag = newVideoTurnDiag(service.recordAccountName(record), "fallback", account, standard, options.framing(), options.thinking(service.settings().VideoThinkingLevel), options.language())
		video, err = session.generateVideo(ctx, prompt, account, standard, options.framing(), options.thinking(service.settings().VideoThinkingLevel), options.language(), attachments)
	}
	if err != nil {
		service.noteVideoRefusal(record.ID, err)
		if safeCredentialCode(err) == "no_video_generated" {
			service.observeTurn(ctx, record.ID, session)
		}
		return httpResponse{}, err
	}
	service.noteVideoDelivered(record.ID)
	service.observeTurn(ctx, record.ID, session)
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

// webVideoCapability picks the capability the video turn runs on: the first the
// account offers in its standard mode.
func webVideoCapability(account webAccount) (capability, bool) {
	for _, entry := range account.Capabilities {
		if entry.Mode == 1 {
			return entry, true
		}
	}
	return capability{}, false
}

// webVideoCapabilityFor is the capability a video turn runs on. A mode the
// caller named must be advertised and is never replaced by another model.
// Without one, the configured default mode is used where the account advertises
// it, and the standard mode everywhere else.
func webVideoCapabilityFor(account webAccount, mode, configured int) (capability, error) {
	if mode == 0 {
		for _, entry := range account.Capabilities {
			if configured != 0 && entry.Mode == configured {
				return entry, nil
			}
		}
		if entry, ok := webVideoCapability(account); ok {
			return entry, nil
		}
		return capability{}, failure(404, "account_model_unavailable")
	}
	for _, entry := range account.Capabilities {
		if entry.Mode == mode {
			return entry, nil
		}
	}
	return capability{}, failure(400, "omni_video_mode_unavailable")
}

// webExecutionResult builds the envelope the host expects from an execution.
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
