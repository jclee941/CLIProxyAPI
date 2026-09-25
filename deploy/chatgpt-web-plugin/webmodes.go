package main

import "strings"

// The web model picker offers the presets WebGPT drives by hand: Instant,
// Medium, High, Extra High and Pro. /backend-api/models publishes each as a
// model slug plus a thinking_effort under versions[latest].intelligence_presets,
// and the conversation body carries that effort as a top-level field the
// backend validates - an unknown value is answered 422 "Invalid conversation
// body" before any generation starts.
const (
	webThinkingModel = "gpt-5-6-thinking"
	webInstantModel  = "gpt-5-6-instant"
)

// webMode is the web turn a request resolves to; an empty Effort is omitted.
type webMode struct {
	Model  string
	Effort string
}

// webEfforts accepts the OpenAI reasoning_effort levels and the m/h/xh/p mode
// names WebGPT uses. low and minimal select the Light effort the thinking model
// also lists; auto keeps the product's own routing.
var webEfforts = map[string]webMode{
	"auto":    {Model: webUpstreamModel},
	"none":    {Model: webInstantModel},
	"minimal": {Model: webThinkingModel, Effort: "min"},
	"low":     {Model: webThinkingModel, Effort: "min"},
	"medium":  {Model: webThinkingModel, Effort: "standard"},
	"m":       {Model: webThinkingModel, Effort: "standard"},
	"high":    {Model: webThinkingModel, Effort: "extended"},
	"h":       {Model: webThinkingModel, Effort: "extended"},
	"xhigh":   {Model: webThinkingModel, Effort: "max"},
	"xh":      {Model: webThinkingModel, Effort: "max"},
	"pro":     {Model: webProModel},
	"p":       {Model: webProModel},
}

func webModelParts(model string) (string, string) {
	base := imagesModelBase(model)
	open := strings.LastIndex(base, "(")
	if open <= 0 || !strings.HasSuffix(base, ")") {
		return base, ""
	}
	return strings.TrimSpace(base[:open]), strings.TrimSpace(base[open+1 : len(base)-1])
}

// webChatMode resolves the web turn a chat request asks for. gpt-6-pro has a
// single effort, so a requested level is moot there. For gpt-web-chat a model
// suffix overrides the body's reasoning_effort, as it does for the host's own
// providers, and asking for neither keeps the product's auto routing.
func webChatMode(model string, request webChatRequest) (webMode, error) {
	base, effort := webModelParts(model)
	if base == webProModel {
		return webMode{Model: webProModel}, nil
	}
	if effort == "" {
		effort = request.ReasoningEffort
	}
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		return webMode{Model: webUpstreamModel}, nil
	}
	mode, ok := webEfforts[effort]
	if !ok {
		return webMode{}, &publicError{HTTPStatus: 400, Code: "unsupported_reasoning_effort",
			Message: "unsupported_reasoning_effort: use none, low, medium, high, xhigh or pro"}
	}
	return mode, nil
}

func (mode webMode) reasoning() bool {
	return mode.Effort != "" || mode.Model == webProModel
}
