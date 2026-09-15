package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Chat runs through the same web conversation the image path uses, because that
// spends the web allowance rather than the Codex API one. The reply arrives as a
// stream of patches; the accumulated text is preferred, and the conversation is
// re-read only when the stream yielded nothing.

const webChatModel = "gpt-web-chat"

func webChatModels() []modelInfo {
	return []modelInfo{{
		ID:                        webChatModel,
		Object:                    "model",
		OwnedBy:                   provider,
		Type:                      "openai",
		DisplayName:               "ChatGPT Web Chat",
		Name:                      webChatModel,
		Description:               "Text generation through the ChatGPT web conversation session; spends the web allowance instead of the Codex API allowance",
		SupportedInputModalities:  []string{"text"},
		SupportedOutputModalities: []string{"text"},
	}}
}

func claimsWebChatModel(model string) bool {
	return imagesModelBase(model) == webChatModel
}

type webChatRequest struct {
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

// webChatPrompt flattens the conversation into the single turn the web product
// accepts, marking the roles so the model can still tell them apart.
func webChatPrompt(payload []byte) (string, error) {
	var request webChatRequest
	if json.Unmarshal(payload, &request) != nil {
		return "", failure(400, "invalid_chat_request")
	}
	var sections []string
	for _, message := range request.Messages {
		text, ok := webMessageText(message.Content)
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		switch message.Role {
		case "system":
			sections = append(sections, "[System instruction]: "+text)
		case "assistant":
			sections = append(sections, "[Assistant]: "+text)
		default:
			sections = append(sections, text)
		}
	}
	prompt := strings.Join(sections, "\n\n")
	if strings.TrimSpace(prompt) == "" {
		return "", failure(400, "prompt_required")
	}
	return prompt, nil
}

func webMessageText(raw json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, true
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return "", false
	}
	var builder strings.Builder
	for _, part := range parts {
		if part.Type == "text" || part.Type == "input_text" {
			builder.WriteString(part.Text)
		}
	}
	return builder.String(), true
}

// webReadReply accumulates the assistant text from the event stream. The stream
// carries either whole message objects or append patches addressed by JSON
// pointer, so both are folded into the same buffer.
func webReadReply(response *http.Response) (string, string) {
	defer closeBody(response)
	conversationID := ""
	text := ""
	scanner := bufio.NewScanner(response.Body)
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
		if conversationID == "" {
			if match := webConversationRE.FindStringSubmatch(payload); len(match) == 2 {
				conversationID = match[1]
			}
		}
		var event struct {
			Message *struct {
				Author struct {
					Role string `json:"role"`
				} `json:"author"`
				Content struct {
					ContentType string   `json:"content_type"`
					Parts       []string `json:"parts"`
				} `json:"content"`
			} `json:"message"`
			Pointer   string          `json:"p"`
			Operation string          `json:"o"`
			Value     json.RawMessage `json:"v"`
		}
		if json.Unmarshal([]byte(payload), &event) != nil {
			continue
		}
		if message := event.Message; message != nil {
			if message.Author.Role == "assistant" && message.Content.ContentType == "text" && len(message.Content.Parts) > 0 {
				text = message.Content.Parts[0]
			}
			continue
		}
		if event.Operation == "append" || event.Pointer == "" {
			var fragment string
			if json.Unmarshal(event.Value, &fragment) == nil && fragment != "" {
				text += fragment
			}
		}
	}
	return text, conversationID
}

// webFinalReply re-reads the conversation when the stream produced no text, so a
// patch shape this plugin does not recognise still yields an answer.
func (client *webClient) webFinalReply(ctx context.Context, conversationID string) (string, error) {
	if conversationID == "" {
		return "", failure(502, "web_conversation_missing")
	}
	path := "/backend-api/conversation/" + conversationID
	deadline := time.Now().Add(webPollBudget)
	for time.Now().Before(deadline) {
		raw, err := client.call(ctx, http.MethodGet, path,
			client.header(path, map[string]string{"Accept": "application/json"}), nil, "web_conversation_read_failed")
		if err == nil {
			if text := webLatestAssistantText(raw); text != "" {
				return text, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", failure(499, "web_client_disconnected")
		case <-time.After(webPollInterval):
		}
	}
	return "", failure(504, "web_reply_not_ready")
}

// webLatestAssistantText picks the newest assistant turn out of the conversation
// mapping, which is keyed by message id rather than ordered.
func webLatestAssistantText(raw []byte) string {
	var conversation struct {
		Mapping map[string]struct {
			Message *struct {
				Author struct {
					Role string `json:"role"`
				} `json:"author"`
				CreateTime float64 `json:"create_time"`
				Content    struct {
					ContentType string   `json:"content_type"`
					Parts       []string `json:"parts"`
				} `json:"content"`
			} `json:"message"`
		} `json:"mapping"`
	}
	if json.Unmarshal(raw, &conversation) != nil {
		return ""
	}
	text, newest := "", -1.0
	for _, node := range conversation.Mapping {
		message := node.Message
		if message == nil || message.Author.Role != "assistant" || message.Content.ContentType != "text" {
			continue
		}
		if len(message.Content.Parts) == 0 || message.Content.Parts[0] == "" {
			continue
		}
		if message.CreateTime >= newest {
			text, newest = message.Content.Parts[0], message.CreateTime
		}
	}
	return text
}

func (client *webClient) generateReply(ctx context.Context, prompt string) (string, error) {
	if err := client.bootstrap(ctx); err != nil {
		return "", err
	}
	requirements, err := client.chatRequirements(ctx)
	if err != nil {
		return "", err
	}
	conduitToken, err := client.prepareConversation(ctx, prompt, requirements)
	if err != nil {
		return "", err
	}
	response, err := client.startGeneration(ctx, prompt, requirements, conduitToken)
	if err != nil {
		return "", err
	}
	text, conversationID := webReadReply(response)
	if strings.TrimSpace(text) != "" {
		return text, nil
	}
	return client.webFinalReply(ctx, conversationID)
}

// executeChat walks the ChatGPT credentials until one completes a web
// conversation turn, mirroring how the image path spreads across accounts.
func (service *service) executeChat(ctx context.Context, raw []byte) (interface{}, error) {
	var request executorRequest
	if json.Unmarshal(raw, &request) != nil {
		return nil, failure(400, "invalid_execution_request")
	}
	if !claimsWebChatModel(request.Model) {
		return nil, failure(400, "unsupported_model")
	}
	payload := request.Payload
	if len(payload) == 0 {
		payload = request.OriginalRequest
	}
	prompt, err := webChatPrompt(payload)
	if err != nil {
		return nil, err
	}
	if request.HostCallbackID == "" {
		return nil, failure(401, "authenticated_execution_callback_required")
	}
	candidates, err := service.imageCandidates(request.HostCallbackID)
	if err != nil {
		return nil, err
	}
	start := int(nextImageCredential.Add(1)-1) % len(candidates)
	var lastErr error = failure(503, "web_chat_unavailable")
	for offset := range candidates {
		entry := candidates[(start+offset)%len(candidates)]
		token, tokenErr := service.tokenFor(request.HostCallbackID, entry)
		if tokenErr != nil {
			lastErr = tokenErr
			continue
		}
		client, clientErr := newWebClient(token)
		if clientErr != nil {
			lastErr = clientErr
			continue
		}
		text, generateErr := client.generateReply(ctx, prompt)
		if generateErr != nil {
			lastErr = generateErr
			continue
		}
		body, renderErr := webChatPayload(request.Model, text)
		if renderErr != nil {
			return nil, renderErr
		}
		return executorResponse{Payload: body, Headers: http.Header{"Content-Type": []string{"application/json"}}}, nil
	}
	return nil, lastErr
}

func webChatPayload(model, text string) ([]byte, error) {
	body := map[string]interface{}{
		"id":      "chatcmpl-web",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []interface{}{map[string]interface{}{
			"index":         0,
			"message":       map[string]interface{}{"role": "assistant", "content": text},
			"finish_reason": "stop",
		}},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, failure(500, "web_response_invalid")
	}
	return raw, nil
}
