package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

type requestInterceptResponse struct {
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
	if err == nil && request.SourceFormat == "gemini" && !request.Stream && validateOmni(request.Body) == nil {
		return requestInterceptResponse{}
	}
	return requestInterceptResponse{
		Terminate:       true,
		StatusCode:      http.StatusBadRequest,
		ResponseHeaders: http.Header{"Content-Type": {"application/json"}},
		ResponseBody:    []byte(`{"error":{"code":"unsupported_omni_request","type":"invalid_request_error","message":"Omni requires a synchronous Gemini-native single-user-turn text request."}}`),
	}
}

func isOmniModel(model string) bool {
	if lastOpen := strings.LastIndex(model, "("); lastOpen >= 0 && strings.HasSuffix(model, ")") {
		return model[:lastOpen] == omniModel
	}
	return model == omniModel
}
