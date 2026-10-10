package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func flowTextCall(service *service, record storageRecord, method, body string) (httpResponse, error) {
	return service.flowTextHTTP(context.Background(), record, flowTestProject, flowHTTPRequest{
		Method: method, Path: "/v1/flow/projects/" + flowTestProject + "/text:generate", Body: []byte(body),
	})
}

func flowTextReply(t *testing.T, parts ...any) string {
	t.Helper()
	candidate := []any{nil, []any{parts, "model"}}
	return rpcEnvelope(t, flowTextRPC, []any{nil, nil, nil, []any{candidate}})
}

func flowTextFailure(t *testing.T, err error) (string, int) {
	t.Helper()
	var public *publicError
	if !errors.As(err, &public) {
		t.Fatalf("error is not public: %v", err)
	}
	return public.Code, public.HTTPStatus
}

func TestFlowTextRequestUsesGenerateContentWirePositions(t *testing.T) {
	// Given a reply with answer text, a thought part and an inline part.
	fixture := newFlowFixture(t)
	service, record := flowService(t, fixture)
	fixture.reply(flowTextRPC, flowTextReply(t, []any{"alpha "}, []any{"hidden", nil, nil, nil, true}, []any{nil, []any{"image/png", "ignored"}}, []any{" beta\n"}))
	image := base64.StdEncoding.EncodeToString(flowTestPNG)
	video := base64.StdEncoding.EncodeToString(flowTestMP4)
	audio := base64.StdEncoding.EncodeToString([]byte("RIFFfixture"))
	body, err := json.Marshal(map[string]any{
		"prompt": "  fixture prompt \n", "systemInstruction": " fixture system ", "thinkingLevel": "high",
		"images": []any{map[string]any{"base64": image, "mimeType": "image/webp"}},
		"videos": []any{map[string]any{"base64": video}},
		"audios": []any{map[string]any{"base64": audio, "mimeType": "  "}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// When the consumer generates text.
	response, err := flowTextCall(service, record, http.MethodPost, string(body))
	if err != nil {
		t.Fatal(err)
	}
	// Then the RPC carries each field at its wire position and only answer text returns.
	var result map[string]string
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatal(err)
	}
	args := fixture.args(flowTextRPC, 0)
	if response.StatusCode != 200 || len(result) != 2 || result["text"] != "alpha  beta\n" || result["model"] != flowTextModel {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	if len(args) != 16 || args[0] != flowTextModel || jsonField(args, 9, 0, 1) != "user" {
		t.Fatalf("args=%v", args)
	}
	if jsonField(args, 9, 0, 0, 0, 0) != "fixture prompt" ||
		jsonField(args, 9, 0, 0, 1, 1, 0) != "image/webp" || jsonField(args, 9, 0, 0, 1, 1, 1) != image ||
		jsonField(args, 9, 0, 0, 2, 1, 0) != "video/mp4" || jsonField(args, 9, 0, 0, 2, 1, 1) != video ||
		jsonField(args, 9, 0, 0, 3, 1, 0) != "audio/wav" || jsonField(args, 9, 0, 0, 3, 1, 1) != audio ||
		len(jsonField(args, 9, 0, 0).([]any)) != 4 {
		t.Fatalf("contents=%v", jsonField(args, 9))
	}
	if jsonField(args, 11, 0, 0, 0) != "fixture system" || jsonField(args, 11, 0, 1) != nil || jsonField(args, 12, 2) != float64(4) {
		t.Fatalf("system=%v thinking=%v", args[11], args[12])
	}
	if jsonField(args, 15, 0) != "token-1" || jsonField(args, 15, 1) != float64(1) || args[14] != nil {
		t.Fatalf("captcha=%v metadata=%v", args[15], args[14])
	}
	if len(fixture.tasks) != 1 || fixture.tasks[0]["pageAction"] != flowTextAction {
		t.Fatalf("captcha tasks=%v", fixture.tasks)
	}
}

func TestFlowTextThinkingLevelsUseWireEnum(t *testing.T) {
	for level, want := range map[string]any{"": nil, "low": float64(2), "medium": float64(3), "high": float64(4)} {
		t.Run("level="+level, func(t *testing.T) {
			// Given a prompt with the level, or none.
			fixture := newFlowFixture(t)
			service, record := flowService(t, fixture)
			fixture.reply(flowTextRPC, flowTextReply(t, []any{"ok"}))
			body := `{"prompt":"fixture"}`
			if level != "" {
				body = `{"prompt":"fixture","thinkingLevel":"` + level + `"}`
			}
			// When it is generated.
			if _, err := flowTextCall(service, record, http.MethodPost, body); err != nil {
				t.Fatal(err)
			}
			// Then the thinking message and system content appear only when requested.
			args := fixture.args(flowTextRPC, 0)
			if jsonField(args, 12, 2) != want || (want == nil) != (args[12] == nil) || args[11] != nil {
				t.Fatalf("thinking=%v system=%v", args[12], args[11])
			}
		})
	}
}

func TestFlowTextRejectsInvalidInputBeforeAnyUpstreamCall(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString([]byte("fixture"))
	cases := map[string]struct{ body, code string }{
		"empty body":             {``, "flow_text_request_invalid"},
		"trailing value":         {`{"prompt":"fixture"} {}`, "flow_text_request_invalid"},
		"unknown field":          {`{"prompt":"fixture","model":"other"}`, "flow_text_request_invalid"},
		"prompt wrong type":      {`{"prompt":7}`, "flow_text_request_invalid"},
		"missing prompt":         {`{}`, "flow_text_prompt_invalid"},
		"blank prompt":           {`{"prompt":" \n\t "}`, "flow_text_prompt_invalid"},
		"prompt too long":        {`{"prompt":"` + strings.Repeat("a", flowTextMaxPrompt+1) + `"}`, "flow_text_prompt_invalid"},
		"system wrong type":      {`{"prompt":"fixture","systemInstruction":[]}`, "flow_text_request_invalid"},
		"unknown thinking":       {`{"prompt":"fixture","thinkingLevel":"extreme"}`, "flow_text_thinking_level_invalid"},
		"empty thinking":         {`{"prompt":"fixture","thinkingLevel":""}`, "flow_text_thinking_level_invalid"},
		"images not array":       {`{"prompt":"fixture","images":"x"}`, "flow_text_request_invalid"},
		"image not object":       {`{"prompt":"fixture","images":["x"]}`, "flow_text_request_invalid"},
		"image unknown field":    {`{"prompt":"fixture","images":[{"base64":"` + valid + `","url":"x"}]}`, "flow_text_request_invalid"},
		"image missing data":     {`{"prompt":"fixture","images":[{"mimeType":"image/png"}]}`, "flow_text_media_invalid"},
		"image empty data":       {`{"prompt":"fixture","images":[{"base64":""}]}`, "flow_text_media_invalid"},
		"video invalid base64":   {`{"prompt":"fixture","videos":[{"base64":"!!!"}]}`, "flow_text_media_invalid"},
		"audio mime wrong type":  {`{"prompt":"fixture","audios":[{"base64":"` + valid + `","mimeType":3}]}`, "flow_text_request_invalid"},
		"mime control character": {`{"prompt":"fixture","images":[{"base64":"` + valid + `","mimeType":"image/\u0000png"}]}`, "flow_text_media_invalid"},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			// Given invalid input.
			fixture := newFlowFixture(t)
			service, record := flowService(t, fixture)
			// When it is submitted.
			_, err := flowTextCall(service, record, http.MethodPost, test.body)
			// Then it is refused as a client error with no captcha spent and no RPC sent.
			code, status := flowTextFailure(t, err)
			if code != test.code || status != 400 || fixture.count(flowTextRPC) != 0 || len(fixture.tasks) != 0 {
				t.Fatalf("code=%s status=%d rpc=%d tasks=%d", code, status, fixture.count(flowTextRPC), len(fixture.tasks))
			}
		})
	}
}

func TestFlowTextPromptLimitCountsUTF16UnitsAfterJSTrim(t *testing.T) {
	for name, test := range map[string]struct {
		prompt string
		valid  bool
	}{
		"limit ascii":      {strings.Repeat("a", flowTextMaxPrompt), true},
		"over ascii":       {strings.Repeat("a", flowTextMaxPrompt+1), false},
		"limit surrogates": {strings.Repeat("\U0001F600", flowTextMaxPrompt/2), true},
		"over surrogates":  {strings.Repeat("\U0001F600", flowTextMaxPrompt/2+1), false},
		"trim before size": {" \uFEFF" + strings.Repeat("a", flowTextMaxPrompt) + "\u00a0 ", true},
		"only bom":         {"\uFEFF", false},
	} {
		t.Run(name, func(t *testing.T) {
			// Given a prompt at a UTF-16 length boundary.
			body, err := json.Marshal(map[string]string{"prompt": test.prompt})
			if err != nil {
				t.Fatal(err)
			}
			// When it is parsed.
			_, err = parseFlowText(body)
			// Then it is accepted or refused as the SDK's trimmed JS length would be.
			if (err == nil) != test.valid {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestFlowTextRoutesOnlyPost(t *testing.T) {
	// Given a configured account.
	fixture := newFlowFixture(t)
	service, record := flowService(t, fixture)
	// When the handler gets another method or a malformed project.
	_, methodErr := flowTextCall(service, record, http.MethodGet, `{"prompt":"fixture"}`)
	_, projectErr := service.flowTextHTTP(context.Background(), record, "project", flowHTTPRequest{Method: http.MethodPost, Body: []byte(`{"prompt":"fixture"}`)})
	// Then neither reaches Flow.
	for _, err := range []error{methodErr, projectErr} {
		if code, status := flowTextFailure(t, err); code != "flow_route_not_found" || status != 404 {
			t.Fatalf("code=%s status=%d", code, status)
		}
	}
	if fixture.count(flowTextRPC) != 0 || len(fixture.tasks) != 0 {
		t.Fatal("upstream reached")
	}
}

func TestFlowTextMissingOutputIsNeverEmptySuccess(t *testing.T) {
	for name, reply := range map[string]func(*testing.T) string{
		"no candidates": func(t *testing.T) string {
			return rpcEnvelope(t, flowTextRPC, []any{nil, nil, nil, []any{}})
		},
		"no content": func(t *testing.T) string {
			return rpcEnvelope(t, flowTextRPC, []any{nil, nil, nil, []any{[]any{}}})
		},
		"thought only": func(t *testing.T) string {
			return flowTextReply(t, []any{"hidden", nil, nil, nil, true})
		},
		"empty text": func(t *testing.T) string { return flowTextReply(t, []any{""}) },
		"non-text part": func(t *testing.T) string {
			return flowTextReply(t, []any{nil, []any{"image/png", "ignored"}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Given a reply that carries no answer text.
			fixture := newFlowFixture(t)
			service, record := flowService(t, fixture)
			fixture.reply(flowTextRPC, reply(t))
			// When text is generated.
			_, err := flowTextCall(service, record, http.MethodPost, `{"prompt":"fixture"}`)
			// Then the call fails and is not resent.
			if code, status := flowTextFailure(t, err); code != "flow_text_output_missing" || status != 502 || fixture.count(flowTextRPC) != 1 {
				t.Fatalf("code=%s status=%d rpc=%d", code, status, fixture.count(flowTextRPC))
			}
		})
	}
	t.Run("non-string text", func(t *testing.T) {
		fixture := newFlowFixture(t)
		service, record := flowService(t, fixture)
		fixture.reply(flowTextRPC, flowTextReply(t, []any{7}))
		_, err := flowTextCall(service, record, http.MethodPost, `{"prompt":"fixture"}`)
		if code, status := flowTextFailure(t, err); code != "flow_response_invalid" || status != 502 {
			t.Fatalf("code=%s status=%d", code, status)
		}
	})
}

func TestFlowTextSurfacesProviderRefusalWithoutRetry(t *testing.T) {
	// Given Flow refusing the prompt.
	fixture := newFlowFixture(t)
	service, record := flowService(t, fixture)
	fixture.reply(flowTextRPC, flowErrorEnvelope(t, []any{"wrb.fr", flowTextRPC, nil, nil, nil, []any{3, nil, []any{"PUBLIC_ERROR_UNSAFE_GENERATION"}}, "generic"}))
	// When text is generated.
	_, err := flowTextCall(service, record, http.MethodPost, `{"prompt":"fixture"}`)
	// Then the refusal is reported once.
	if code, status := flowTextFailure(t, err); code != "flow_request_rejected" || status != 400 || fixture.count(flowTextRPC) != 1 {
		t.Fatalf("code=%s status=%d rpc=%d", code, status, fixture.count(flowTextRPC))
	}
}

func TestFlowTextReplacesOnlyRefusedCaptchaTokens(t *testing.T) {
	refusal := func() string {
		return flowErrorEnvelope(t, []any{"wrb.fr", flowTextRPC, nil, nil, nil, []any{3, nil, []any{"PUBLIC_ERROR_UNUSUAL_ACTIVITY"}}, "generic"})
	}
	t.Run("recovers with a fresh token", func(t *testing.T) {
		// Given one refused token followed by an answer.
		fixture := newFlowFixture(t)
		service, record := flowService(t, fixture)
		fixture.reply(flowTextRPC, refusal(), flowTextReply(t, []any{"ok"}))
		// When text is generated.
		response, err := flowTextCall(service, record, http.MethodPost, `{"prompt":"fixture"}`)
		// Then the second call carries a new token and the answer returns.
		if err != nil || response.StatusCode != 200 || fixture.count(flowTextRPC) != 2 ||
			jsonField(fixture.args(flowTextRPC, 0), 15, 0) != "token-1" || jsonField(fixture.args(flowTextRPC, 1), 15, 0) != "token-2" {
			t.Fatalf("err=%v response=%s rpc=%d", err, response.Body, fixture.count(flowTextRPC))
		}
	})
	t.Run("stops at the broker attempt limit", func(t *testing.T) {
		// Given every token refused.
		fixture := newFlowFixture(t)
		service, record := flowService(t, fixture)
		fixture.reply(flowTextRPC, refusal())
		// When text is generated.
		_, err := flowTextCall(service, record, http.MethodPost, `{"prompt":"fixture"}`)
		// Then the refusal surfaces after the known attempts.
		if code, status := flowTextFailure(t, err); code != "flow_captcha_rejected" || status != 503 || fixture.count(flowTextRPC) != flowTokenAttempts {
			t.Fatalf("code=%s status=%d rpc=%d", code, status, fixture.count(flowTextRPC))
		}
	})
	t.Run("lost answer is not resent", func(t *testing.T) {
		// Given a connection dropped after the request was sent.
		fixture := newFlowFixture(t)
		service, record := flowService(t, fixture)
		fixture.dropRPC = flowTextRPC
		// When text is generated.
		_, err := flowTextCall(service, record, http.MethodPost, `{"prompt":"fixture"}`)
		// Then the outcome is reported unknown after a single send.
		if code, status := flowTextFailure(t, err); code != "flow_submission_outcome_unknown" || status != 502 || fixture.count(flowTextRPC) != 1 {
			t.Fatalf("code=%s status=%d rpc=%d", code, status, fixture.count(flowTextRPC))
		}
	})
}
