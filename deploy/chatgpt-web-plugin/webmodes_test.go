package main

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestWebChatModeMapsEffortsOntoTheWebPresets(t *testing.T) {
	cases := map[string]webMode{
		"":        {Model: webUpstreamModel},
		"auto":    {Model: webUpstreamModel},
		"none":    {Model: webInstantModel},
		"minimal": {Model: webThinkingModel, Effort: "min"},
		"low":     {Model: webThinkingModel, Effort: "min"},
		"medium":  {Model: webThinkingModel, Effort: "standard"},
		"m":       {Model: webThinkingModel, Effort: "standard"},
		"High":    {Model: webThinkingModel, Effort: "extended"},
		"h":       {Model: webThinkingModel, Effort: "extended"},
		"xhigh":   {Model: webThinkingModel, Effort: "max"},
		"xh":      {Model: webThinkingModel, Effort: "max"},
		"pro":     {Model: webProModel},
		"p":       {Model: webProModel},
	}
	for effort, want := range cases {
		got, err := webChatMode(webChatModel, webChatRequest{ReasoningEffort: effort})
		if err != nil || got != want {
			t.Fatalf("effort %q: got %+v, err %v, want %+v", effort, got, err, want)
		}
	}
}

func TestWebChatModeLetsTheModelSuffixOverrideTheBody(t *testing.T) {
	got, err := webChatMode("chatgpt-web/GPT-Web-Chat(xhigh)", webChatRequest{ReasoningEffort: "low"})
	if err != nil || got != (webMode{Model: webThinkingModel, Effort: "max"}) {
		t.Fatalf("got %+v, err %v", got, err)
	}
}

func TestWebChatModeKeepsProOnItsSingleEffort(t *testing.T) {
	got, err := webChatMode("gpt-6-pro(high)", webChatRequest{ReasoningEffort: "xhigh"})
	if err != nil || got != (webMode{Model: webProModel}) {
		t.Fatalf("got %+v, err %v", got, err)
	}
}

func TestWebChatModeRefusesAnEffortTheProductHasNot(t *testing.T) {
	_, err := webChatMode(webChatModel, webChatRequest{ReasoningEffort: "ultra"})
	var public *publicError
	if !errors.As(err, &public) || public.HTTPStatus != 400 || public.Code != "unsupported_reasoning_effort" {
		t.Fatalf("err = %v", err)
	}
}

func TestSuffixedChatModelsAreClaimedAndRouted(t *testing.T) {
	for _, model := range []string{"gpt-web-chat(xhigh)", "gpt-6-pro(p)"} {
		if !claimsWebChatModel(model) {
			t.Fatalf("%s was not claimed", model)
		}
		raw, err := json.Marshal(modelRouteRequest{SourceFormat: "openai", RequestedModel: model})
		if err != nil {
			t.Fatal(err)
		}
		result, err := routeImages(raw)
		if err != nil {
			t.Fatal(err)
		}
		if response := decodeRouteResponse(t, result); !response.Handled || response.TargetKind != "self" {
			t.Fatalf("%s: %+v", model, response)
		}
	}
}

func TestPayloadNamesThePresetTheProductServed(t *testing.T) {
	raw, err := webChatPayload(webChatModel, webReply{Text: "16", Model: webThinkingModel, Effort: "max"}, false)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Model             string `json:"model"`
		SystemFingerprint string `json:"system_fingerprint"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.Model != webChatModel || body.SystemFingerprint != "gpt-5-6-thinking/max" {
		t.Fatalf("body = %+v", body)
	}
}

func TestTurnCarriesAThinkingEffortOnlyWhenOneWasAsked(t *testing.T) {
	thinking := map[string]interface{}{}
	webTurn{Mode: webMode{Model: webThinkingModel, Effort: "max"}}.fill(thinking)
	if thinking["model"] != webThinkingModel || thinking["thinking_effort"] != "max" {
		t.Fatalf("thinking body = %+v", thinking)
	}

	auto := map[string]interface{}{}
	webTurn{Mode: webMode{Model: webUpstreamModel}, Hints: []string{"picture_v2"}}.fill(auto)
	if _, present := auto["thinking_effort"]; present {
		t.Fatalf("an effort was sent for the auto route: %+v", auto)
	}
	if hints, _ := auto["system_hints"].([]string); len(hints) != 1 || hints[0] != "picture_v2" {
		t.Fatalf("hints = %+v", auto["system_hints"])
	}
}
