package main

import (
	"encoding/json"
	"net/http"
)

const (
	provider             = "chatgpt2api"
	nativeProvider       = "openai-compatible-chatgpt2api"
	version              = "1.0.1"
	statusPath           = "/v0/management/plugins/chatgpt2api/status"
	resourcePath         = "/v0/resource/plugins/chatgpt2api/index"
	defaultDashboardPath = "/CLIProxyAPI/plugins/chatgpt2api/index.html"
)

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *publicError    `json:"error,omitempty"`
}

type publicError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status"`
}

func (failure *publicError) Error() string { return failure.Code }

func failure(status int, code string) *publicError {
	return &publicError{Code: code, Message: code, HTTPStatus: status}
}

func encode[Value any](value Value) (json.RawMessage, *publicError) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, failure(500, "response_encoding_failed")
	}
	return raw, nil
}

type routeRequest struct {
	RequestedModel     string
	AvailableProviders []string
}

type routeResponse struct {
	Handled     bool
	TargetKind  string
	Target      string
	TargetModel string
	Reason      string
}

type managementRequest struct {
	Method string
	Path   string
}

type httpResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}
