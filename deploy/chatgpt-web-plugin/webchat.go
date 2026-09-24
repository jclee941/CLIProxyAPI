package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Chat runs through the same web conversation the image path uses, because that
// spends the web allowance rather than the Codex API one. The reply arrives as a
// stream of patches; the accumulated text is preferred, and the conversation is
// re-read only when the stream yielded nothing.

const webChatModel = "gpt-web-chat"
const webProModel = "gpt-6-pro"

// webChatPollBudget bounds the re-read that runs when the stream produced no
// text. A chat reply is quick, unlike an image, so waiting the image budget here
// would hold the plugin long enough for the host's other calls to time out.
const webChatPollBudget = 45 * time.Second

// webChatMaxCredentials bounds how many accounts one chat turn may try, so a
// systematic failure cannot multiply the wait by the size of the pool.
const webChatMaxCredentials = 2

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
	}, {
		ID:                        webProModel,
		Object:                    "model",
		OwnedBy:                   provider,
		Type:                      "openai",
		DisplayName:               "GPT-6 Pro",
		Name:                      webProModel,
		Description:               "ChatGPT web GPT-6 Pro; spends the web allowance instead of the Codex API allowance",
		SupportedInputModalities:  []string{"text"},
		SupportedOutputModalities: []string{"text"},
	}}
}

func claimsWebChatModel(model string) bool {
	switch imagesModelBase(model) {
	case webChatModel, webProModel:
		return true
	default:
		return false
	}
}

func claimsWebProModel(model string) bool {
	return imagesModelBase(model) == webProModel
}

func webChatUpstreamModel(model string) string {
	if claimsWebProModel(model) {
		return webProModel
	}
	return webUpstreamModel
}

func isChatGPTProCredential(entry hostEntry) bool {
	label := strings.ToLower(entry.ID + " " + entry.Name)
	if strings.Contains(label, "prolite") {
		return false
	}
	return strings.Contains(label, "-pro")
}

func preferProCredentials(entries []hostEntry) []hostEntry {
	pro := make([]hostEntry, 0, len(entries))
	rest := make([]hostEntry, 0, len(entries))
	for _, entry := range entries {
		if isChatGPTProCredential(entry) {
			pro = append(pro, entry)
			continue
		}
		rest = append(rest, entry)
	}
	return append(pro, rest...)
}

type webChatRequest struct {
	Messages []struct {
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		Name      string          `json:"name"`
		ToolCalls []struct {
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	} `json:"tools"`
	ToolChoice json.RawMessage `json:"tool_choice"`
}

// webChatToolPrompt states the call contract, because the web product has no
// function calling of its own. The wording mirrors the block the reply is then
// scanned for.
func webChatToolPrompt(request webChatRequest) string {
	if len(request.Tools) == 0 {
		return ""
	}
	lines := []string{
		"# Tool Use",
		"",
		"Call a tool by replying with only this block:",
		"```tool_call",
		`{"name": "<tool_name>", "arguments": {<arguments>}}`,
		"```",
		"",
		"Available tools:",
	}
	for _, tool := range request.Tools {
		entry := "- " + tool.Function.Name
		if tool.Function.Description != "" {
			entry += ": " + tool.Function.Description
		}
		lines = append(lines, entry)
		if len(tool.Function.Parameters) > 0 {
			lines = append(lines, "  arguments must validate against "+string(tool.Function.Parameters))
		}
	}
	if required, forced := webChatToolChoice(request); required {
		if forced != "" {
			lines = append(lines, "", `IMPORTANT: You MUST call the tool "`+forced+`". Do not reply with text only.`)
		} else {
			lines = append(lines, "", "IMPORTANT: You MUST call at least one tool. Do not reply with text only.")
		}
	}
	return strings.Join(lines, "\n")
}

// webChatToolChoice reports whether the caller demanded a call, and which one.
func webChatToolChoice(request webChatRequest) (bool, string) {
	var named string
	if json.Unmarshal(request.ToolChoice, &named) == nil {
		return strings.EqualFold(strings.TrimSpace(named), "required"), ""
	}
	var specific struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(request.ToolChoice, &specific) == nil && specific.Function.Name != "" {
		return true, specific.Function.Name
	}
	return false, ""
}

// webChatPrompt flattens the conversation into the single turn the web product
// accepts, marking the roles so the model can still tell them apart.
func webChatPrompt(payload []byte) (string, webChatRequest, error) {
	var request webChatRequest
	if json.Unmarshal(payload, &request) != nil {
		return "", request, failure(400, "invalid_chat_request")
	}
	var sections []string
	if instruction := webChatToolPrompt(request); instruction != "" {
		sections = append(sections, instruction)
	}
	for _, message := range request.Messages {
		text, _ := webMessageText(message.Content)
		switch message.Role {
		case "system":
			if strings.TrimSpace(text) != "" {
				sections = append(sections, "[System instruction]: "+text)
			}
		case "assistant":
			rendered := text
			for _, call := range message.ToolCalls {
				rendered += "\n```tool_call\n{\"name\": \"" + call.Function.Name + "\", \"arguments\": " + webArgumentsJSON(call.Function.Arguments) + "}\n```"
			}
			if strings.TrimSpace(rendered) != "" {
				sections = append(sections, "[Assistant]: "+rendered)
			}
		case "tool":
			sections = append(sections, "[Tool result for "+message.Name+"]: "+text)
		default:
			if strings.TrimSpace(text) != "" {
				sections = append(sections, text)
			}
		}
	}
	prompt := strings.Join(sections, "\n\n")
	if strings.TrimSpace(prompt) == "" {
		return "", request, failure(400, "prompt_required")
	}
	return prompt, request, nil
}

func webArgumentsJSON(arguments string) string {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return "{}"
	}
	return trimmed
}

type webToolCall struct {
	Name      string
	Arguments string
}

var webToolCallPattern = regexp.MustCompile("(?s)```tool_call\\s*\\n(.*?)\\n```")

// webParseToolCalls recovers the calls a reply carries, accepting the fenced
// block that was requested as well as a bare object, which the model emits when
// the whole reply is the call.
func webParseToolCalls(text string) (string, []webToolCall) {
	clean := text
	var calls []webToolCall
	for _, match := range webToolCallPattern.FindAllStringSubmatch(clean, -1) {
		if call, ok := webDecodeToolCall(match[1]); ok {
			calls = append(calls, call)
		}
	}
	clean = strings.TrimSpace(webToolCallPattern.ReplaceAllString(clean, ""))
	if len(calls) == 0 && strings.HasPrefix(strings.TrimSpace(clean), "{") {
		if call, ok := webDecodeToolCall(clean); ok {
			calls, clean = append(calls, call), ""
		}
	}
	return clean, calls
}

func webDecodeToolCall(raw string) (webToolCall, bool) {
	var decoded struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Args      json.RawMessage `json:"args"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &decoded) != nil || decoded.Name == "" {
		return webToolCall{}, false
	}
	arguments := decoded.Arguments
	if len(arguments) == 0 {
		arguments = decoded.Args
	}
	if len(arguments) == 0 {
		arguments = json.RawMessage("{}")
	}
	return webToolCall{Name: decoded.Name, Arguments: string(arguments)}, true
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
		webTraceEvent(payload)
		if json.Unmarshal([]byte(payload), &event) != nil {
			continue
		}
		if message := event.Message; message != nil {
			if message.Author.Role == "assistant" && message.Content.ContentType == "text" && len(message.Content.Parts) > 0 {
				// A whole message can arrive after deltas; keep whichever is longer
				// so a partial snapshot cannot discard what was accumulated.
				if len(message.Content.Parts[0]) > len(text) {
					text = message.Content.Parts[0]
				}
			}
			continue
		}
		webAccumulate(json.RawMessage(payload), "", &text)
	}
	return text, conversationID
}

// webTextPointer reports whether a JSON pointer addresses the reply text. An
// empty pointer is inherited from an event that carried no path of its own, which
// the product uses for the plain text delta.
func webTextPointer(pointer string) bool {
	return pointer == "" || strings.HasSuffix(pointer, "/parts/0")
}

// webAccumulate folds one stream event into the reply. The product nests its
// deltas: an event may carry the fragment directly, wrap it in a counter envelope,
// or hold a list of operations, and each level may restate the pointer. Observed
// shapes are {"c":n,"v":{...}}, {"o":"patch","v":[...]}, {"o":"append","p":...,
// "v":"..."} and a bare {"v":"..."}; handling only the flat ones truncates the
// reply at the first nested delta.
func webAccumulate(raw json.RawMessage, pointer string, text *string) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return
	}
	switch trimmed[0] {
	case '"':
		var fragment string
		if json.Unmarshal(raw, &fragment) == nil && fragment != "" && webTextPointer(pointer) {
			*text += fragment
		}
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return
		}
		for _, item := range items {
			webAccumulate(item, pointer, text)
		}
	case '{':
		var node struct {
			Pointer *string         `json:"p"`
			Value   json.RawMessage `json:"v"`
		}
		if json.Unmarshal(raw, &node) != nil || len(node.Value) == 0 {
			return
		}
		next := pointer
		if node.Pointer != nil && *node.Pointer != "" {
			next = *node.Pointer
		}
		webAccumulate(node.Value, next, text)
	}
}

// webChatTrace turns on a one-line-per-event record of the stream's shape. It is
// a build-time switch because the reply patches are undocumented and the only
// way to learn their shapes is to observe a real turn.
const webChatTrace = false

// webTraceEvent records the structure of one stream event: which keys it carries,
// the patch operation and pointer, and the type of the value. The value itself is
// never recorded, so no reply text reaches the log.
func webTraceEvent(payload string) {
	if !webChatTrace {
		return
	}
	var event map[string]json.RawMessage
	if json.Unmarshal([]byte(payload), &event) != nil {
		fmt.Fprintf(os.Stderr, "WEBCHATDBG non-object len=%d head=%.40q\n", len(payload), payload)
		return
	}
	keys := make([]string, 0, len(event))
	for key := range event {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	operation, pointer, kind := "", "", ""
	_ = json.Unmarshal(event["o"], &operation)
	_ = json.Unmarshal(event["p"], &pointer)
	if raw, present := event["v"]; present {
		trimmed := strings.TrimSpace(string(raw))
		switch {
		case strings.HasPrefix(trimmed, `"`):
			kind = "string"
		case strings.HasPrefix(trimmed, "["):
			kind = "array"
		case strings.HasPrefix(trimmed, "{"):
			kind = "object"
		default:
			kind = "scalar"
		}
	}
	fmt.Fprintf(os.Stderr, "WEBCHATDBG keys=%s o=%q p=%q v=%s\n", strings.Join(keys, ","), operation, pointer, kind)
}

// webFinalReply re-reads the conversation when the stream produced no text, so a
// patch shape this plugin does not recognise still yields an answer.
func (client *webClient) webFinalReply(ctx context.Context, conversationID string) (string, error) {
	if conversationID == "" {
		return "", failure(502, "web_conversation_missing")
	}
	path := "/backend-api/conversation/" + conversationID
	deadline := time.Now().Add(webChatPollBudget)
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

func (client *webClient) generateReply(ctx context.Context, prompt, model string) (string, error) {
	if err := client.bootstrap(ctx); err != nil {
		return "", err
	}
	requirements, err := client.chatRequirements(ctx)
	if err != nil {
		return "", err
	}
	conduitToken, err := client.prepareConversation(ctx, prompt, requirements, model, nil)
	if err != nil {
		return "", err
	}
	response, err := client.startGeneration(ctx, prompt, requirements, conduitToken, model, nil)
	if err != nil {
		return "", err
	}
	// The accumulated stream is the reply. Re-reading the conversation first was
	// tried and measured worse: the stored shape this plugin knows how to read is
	// not the one the product returns, so every turn spent the whole poll budget
	// before falling back here anyway.
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
	prompt, request2, err := webChatPrompt(payload)
	if err != nil {
		return nil, err
	}
	toolsOffered := len(request2.Tools) > 0
	if request.HostCallbackID == "" {
		return nil, failure(401, "authenticated_execution_callback_required")
	}
	candidates, err := service.imageCandidates(request.HostCallbackID)
	if err != nil {
		return nil, err
	}
	start := int(nextImageCredential.Add(1)-1) % len(candidates)
	if claimsWebProModel(request.Model) {
		candidates = preferProCredentials(candidates)
		start = 0
	}
	var lastErr error = failure(503, "web_chat_unavailable")
	attempts := min(len(candidates), webChatMaxCredentials)
	upstream := webChatUpstreamModel(request.Model)
	for offset := 0; offset < attempts; offset++ {
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
		text, generateErr := client.generateReply(ctx, prompt, upstream)
		if generateErr != nil {
			lastErr = generateErr
			continue
		}
		body, renderErr := webChatPayload(request.Model, text, toolsOffered)
		if renderErr != nil {
			return nil, renderErr
		}
		return executorResponse{Payload: body, Headers: http.Header{"Content-Type": []string{"application/json"}}}, nil
	}
	return nil, lastErr
}

func webChatPayload(model, text string, toolsOffered bool) ([]byte, error) {
	message := map[string]interface{}{"role": "assistant", "content": text}
	finish := "stop"
	if toolsOffered {
		clean, calls := webParseToolCalls(text)
		if len(calls) > 0 {
			rendered := make([]interface{}, 0, len(calls))
			for index, call := range calls {
				rendered = append(rendered, map[string]interface{}{
					"id":       fmt.Sprintf("call_web_%d", index),
					"type":     "function",
					"function": map[string]string{"name": call.Name, "arguments": call.Arguments},
				})
			}
			message["tool_calls"] = rendered
			message["content"] = nil
			if strings.TrimSpace(clean) != "" {
				message["content"] = clean
			}
			finish = "tool_calls"
		}
	}
	body := map[string]interface{}{
		"id":      "chatcmpl-web",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []interface{}{map[string]interface{}{
			"index":         0,
			"message":       message,
			"finish_reason": finish,
		}},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, failure(500, "web_response_invalid")
	}
	return raw, nil
}
