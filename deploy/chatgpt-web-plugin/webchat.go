package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Chat runs through the same web conversation the image path uses, because that
// spends the web allowance rather than the Codex API one. The reply arrives as a
// stream of patches; the accumulated text is preferred, and the conversation is
// re-read only when the stream did not deliver a whole answer.

const webChatModel = "gpt-web-chat"
const webProModel = "gpt-6-pro"

// webChatPollBudget bounds the re-read that runs when the stream produced no
// text. A chat reply is quick, unlike an image, so waiting the image budget here
// would hold the plugin long enough for the host's other calls to time out.
const webChatPollBudget = 45 * time.Second

// webReasoningPollBudget is that re-read for a thinking or Pro turn, which keeps
// working server-side for minutes after its stream went away. It is the interval
// WebGPT leaves before checking on a chat it delegated.
const webReasoningPollBudget = 20 * time.Minute

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
		Description:               "Text generation through the ChatGPT web conversation session; reasoning_effort none, low, medium, high, xhigh or pro selects the web Instant, Light, Medium, High, Extra High or Pro preset, and no effort keeps the product's auto routing. Spends the web allowance instead of the Codex API allowance",
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
	base, _ := webModelParts(model)
	switch base {
	case webChatModel, webProModel:
		return true
	default:
		return false
	}
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
	ToolChoice      json.RawMessage `json:"tool_choice"`
	ReasoningEffort string          `json:"reasoning_effort"`
}

// webChatToolPrompt states the call contract, because the web product has no
// function calling of its own. The wording mirrors the block the reply is then
// scanned for. Framed as tool use, the web models treat the contract as tools
// they do not have: they refuse, or run the command in their own sandbox. Framed
// as actions an agent carries out for them, Instant, auto and every thinking
// effort answered with the block when measured live.
func webChatToolPrompt(request webChatRequest) string {
	if len(request.Tools) == 0 {
		return ""
	}
	lines := []string{
		"# Response format",
		"",
		"You are the reasoning engine of an agent that runs on the user's machine. You cannot reach that machine yourself; the agent can, and it carries out the actions listed below for you. When the agent has to act before you can answer, reply with only the action for it to run, written as this block and nothing else:",
		"```tool_call",
		`{"name": "<action>", "arguments": {<arguments>}}`,
		"```",
		`The agent runs it and replies with "[Tool result for <action>]: ...". Once no action is needed, reply with the answer in plain text.`,
		"",
		"Actions the agent can run:",
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
			lines = append(lines, "", `IMPORTANT: Reply with the "`+forced+`" action block, not with a plain-text answer.`)
		} else {
			lines = append(lines, "", "IMPORTANT: Reply with an action block, not with a plain-text answer.")
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

type webChatPlan struct {
	model      string
	stream     string
	callbackID string
	turn       webTurn
	tools      bool
	candidates []hostEntry
}

// planChat parses an execution request into the turn to run. The Pro model
// tries Pro credentials first because only they carry its allowance; every
// other turn rotates across the pool.
func (service *service) planChat(raw []byte) (webChatPlan, error) {
	var request executorRequest
	if json.Unmarshal(raw, &request) != nil {
		return webChatPlan{}, failure(400, "invalid_execution_request")
	}
	if !claimsWebChatModel(request.Model) {
		return webChatPlan{}, failure(400, "unsupported_model")
	}
	payload := request.Payload
	if len(payload) == 0 {
		payload = request.OriginalRequest
	}
	prompt, chat, err := webChatPrompt(payload)
	if err != nil {
		return webChatPlan{}, err
	}
	mode, err := webChatMode(request.Model, chat)
	if err != nil {
		return webChatPlan{}, err
	}
	if request.HostCallbackID == "" {
		return webChatPlan{}, failure(401, "authenticated_execution_callback_required")
	}
	entries, err := service.imageCandidates(request.HostCallbackID)
	if err != nil {
		return webChatPlan{}, err
	}
	start := int(nextImageCredential.Add(1)-1) % len(entries)
	if mode.Model == webProModel {
		entries, start = preferProCredentials(entries), 0
	}
	plan := webChatPlan{
		model:      request.Model,
		stream:     request.StreamID,
		callbackID: request.HostCallbackID,
		turn:       webTurn{Prompt: prompt, Mode: mode},
		tools:      len(chat.Tools) > 0,
	}
	for offset := 0; offset < min(len(entries), webChatMaxCredentials); offset++ {
		plan.candidates = append(plan.candidates, entries[(start+offset)%len(entries)])
	}
	return plan, nil
}

func (service *service) clientFor(callbackID string, entry hostEntry) (*webClient, error) {
	token, err := service.tokenFor(callbackID, entry)
	if err != nil {
		return nil, err
	}
	return newWebClient(token)
}

// startReply runs a turn up to the point the product starts answering.
func (client *webClient) startReply(ctx context.Context, turn webTurn) (*http.Response, error) {
	if err := client.bootstrap(ctx); err != nil {
		return nil, err
	}
	requirements, err := client.chatRequirements(ctx)
	if err != nil {
		return nil, err
	}
	conduitToken, err := client.prepareConversation(ctx, turn, requirements)
	if err != nil {
		return nil, err
	}
	return client.startGeneration(ctx, turn, requirements, conduitToken)
}

// finishReply reads the answer, handing each growth to onDelta when one is set.
// The accumulated stream is the reply. Re-reading the conversation first was
// tried and measured worse: the stored shape this plugin knows how to read is
// not the one the product returns, so every turn spent the whole poll budget
// before falling back anyway. The re-read only completes a stream that ended
// without a whole answer, and a thinking turn gets as long as it may still run.
func (client *webClient) finishReply(ctx context.Context, response *http.Response, mode webMode, onDelta func(string) error) (webReply, error) {
	reply, err := webStreamReply(response, onDelta)
	if err != nil {
		return reply, err
	}
	settled := reply.Complete || reply.ConversationID == ""
	if settled && strings.TrimSpace(reply.Text) != "" {
		return reply, nil
	}
	budget := webChatPollBudget
	if mode.reasoning() {
		budget = webReasoningPollBudget
	}
	text, err := client.webFinalReply(ctx, reply.ConversationID, budget)
	if err != nil {
		return reply, err
	}
	rest, extends := strings.CutPrefix(text, reply.Text)
	reply.Text = text
	if onDelta != nil && extends && rest != "" {
		return reply, onDelta(rest)
	}
	return reply, nil
}

func (client *webClient) generateReply(ctx context.Context, turn webTurn) (webReply, error) {
	response, err := client.startReply(ctx, turn)
	if err != nil {
		return webReply{}, err
	}
	return client.finishReply(ctx, response, turn.Mode, nil)
}

// executeChat walks the ChatGPT credentials until one completes a web
// conversation turn, mirroring how the image path spreads across accounts.
func (service *service) executeChat(ctx context.Context, raw []byte) (interface{}, error) {
	plan, err := service.planChat(raw)
	if err != nil {
		return nil, err
	}
	var lastErr error = failure(503, "web_chat_unavailable")
	for _, entry := range plan.candidates {
		client, err := service.clientFor(plan.callbackID, entry)
		if err != nil {
			lastErr = err
			continue
		}
		reply, err := client.generateReply(ctx, plan.turn)
		service.discard(ctx, client, reply.ConversationID)
		if err != nil {
			lastErr = err
			continue
		}
		service.checkServed(plan.turn.Mode, reply)
		body, err := webChatPayload(plan.model, reply, plan.tools)
		if err != nil {
			return nil, err
		}
		return executorResponse{Payload: body, Headers: http.Header{"Content-Type": []string{"application/json"}}}, nil
	}
	return nil, lastErr
}

// checkServed reports a turn the product answered on another model or effort
// than was asked, which is what it does once an allowance runs out. WebGPT
// checks the picker before it sends; the stream is where this plugin can see
// the same thing. The product's own auto routing asks for nothing to check.
func (service *service) checkServed(mode webMode, reply webReply) {
	if mode.Model == webUpstreamModel || reply.Model == "" {
		return
	}
	if reply.Model == mode.Model && reply.Effort == mode.Effort {
		return
	}
	service.report("chatgpt-web: turn served on a different mode", map[string]any{
		"provider": provider, "model": mode.Model, "level": mode.Effort, "state": reply.Model + " " + reply.Effort,
	})
}

// served names the preset the product answered on, such as gpt-5-6-thinking/max.
// Responses carry it as system_fingerprint, so a caller can see which preset ran
// and not only which one it asked for.
func (reply webReply) served() string {
	if reply.Effort == "" {
		return reply.Model
	}
	return reply.Model + "/" + reply.Effort
}

func webChatPayload(model string, reply webReply, toolsOffered bool) ([]byte, error) {
	message := map[string]interface{}{"role": "assistant", "content": reply.Text}
	finish := "stop"
	if toolsOffered {
		clean, calls := webParseToolCalls(reply.Text)
		if len(calls) > 0 {
			message["tool_calls"] = webRenderToolCalls(calls, false)
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
	if served := reply.served(); served != "" {
		body["system_fingerprint"] = served
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, failure(500, "web_response_invalid")
	}
	return raw, nil
}

func webRenderToolCalls(calls []webToolCall, streamed bool) []interface{} {
	rendered := make([]interface{}, 0, len(calls))
	for index, call := range calls {
		item := map[string]interface{}{
			"id":       fmt.Sprintf("call_web_%d", index),
			"type":     "function",
			"function": map[string]string{"name": call.Name, "arguments": call.Arguments},
		}
		if streamed {
			item["index"] = index
		}
		rendered = append(rendered, item)
	}
	return rendered
}
