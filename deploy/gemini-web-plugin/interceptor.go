package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type requestInterceptResponse struct {
	Headers         http.Header `json:"Headers,omitempty"`
	ClearHeaders    []string    `json:"ClearHeaders,omitempty"`
	Terminate       bool        `json:"Terminate,omitempty"`
	StatusCode      int         `json:"StatusCode,omitempty"`
	ResponseHeaders http.Header `json:"ResponseHeaders,omitempty"`
	ResponseBody    []byte      `json:"ResponseBody,omitempty"`
}

func interceptRequest(raw []byte) requestInterceptResponse {
	var request struct {
		SourceFormat, RequestedModel, Model string
		Stream                              bool
		Body                                []byte
	}
	err := json.Unmarshal(raw, &request)
	if !isOmniModel(request.RequestedModel) && !isOmniModel(request.Model) {
		return requestInterceptResponse{}
	}
	if err != nil || request.Stream {
		return omniRejection(nil)
	}
	if routeErr := omniRouteRejection(request.SourceFormat, request.Body); routeErr != nil {
		return omniRejection(routeErr)
	}
	return requestInterceptResponse{}
}

const omniGenericMessage = "Omni requires a synchronous single-user-turn text request in Gemini or OpenAI chat format."

// A rejected option is named so the caller can correct it; every other refusal
// keeps the generic wording rather than describing an internal check.
var omniRejectionMessages = map[string]string{
	"omni_unsupported_generation_option": "Omni accepts only aspectRatio, negativePrompt and candidateCount in generationConfig.",
	"omni_invalid_aspect_ratio":          "Omni video framing is an orientation, so aspectRatio accepts 16:9 or 9:16.",
	"omni_invalid_negative_prompt":       "Omni requires negativePrompt to be a string.",
	"omni_single_candidate_only":         "Omni produces exactly one video per request.",
	"omni_prompt_length_invalid":         "Omni requires a non-empty prompt of at most 8000 characters, including the folded generation options.",
}

func omniRejection(err error) requestInterceptResponse {
	code, message := "unsupported_omni_request", omniGenericMessage
	var public *publicError
	if errors.As(err, &public) {
		if named, ok := omniRejectionMessages[public.Code]; ok {
			code, message = public.Code, named
		}
	}
	body, marshalErr := json.Marshal(map[string]any{
		"error": map[string]string{"code": code, "type": "invalid_request_error", "message": message},
	})
	if marshalErr != nil {
		body = []byte(`{"error":{"code":"unsupported_omni_request","type":"invalid_request_error","message":"` + omniGenericMessage + `"}}`)
	}
	return requestInterceptResponse{
		Terminate:       true,
		StatusCode:      http.StatusBadRequest,
		ResponseHeaders: http.Header{"Content-Type": {"application/json"}},
		ResponseBody:    body,
	}
}

// omniRouteRejection mirrors the executor's own admission checks so a malformed
// request is refused before any video is produced.
func omniRouteRejection(sourceFormat string, body []byte) error {
	switch sourceFormat {
	case "gemini":
		return validateOmni(body)
	case "openai":
		_, err := openAIPromptForOmni(body)
		return err
	}
	return failure(400, "unsupported_omni_request")
}

func isOmniModel(model string) bool {
	if lastOpen := strings.LastIndex(model, "("); lastOpen >= 0 && strings.HasSuffix(model, ")") {
		return model[:lastOpen] == omniModel
	}
	return model == omniModel
}
