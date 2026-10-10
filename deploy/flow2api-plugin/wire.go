package main

import (
	"encoding/json"
	"errors"
	"net/http"
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

func (failure *publicError) Error() string         { return failure.Message }
func failure(status int, code string) *publicError { return &publicError{code, code, status} }
func safeCredentialCode(err error) string {
	if err == nil {
		return ""
	}
	var public *publicError
	if errors.As(err, &public) {
		return public.Code
	}
	return "flow_operation_failed"
}
func safeCredentialMessage(err error) string {
	if err == nil {
		return ""
	}
	var public *publicError
	if errors.As(err, &public) {
		return public.Message
	}
	return "flow_operation_failed"
}

type storageRecord struct {
	Type         string `json:"type"`
	ID           string `json:"id"`
	Label        string `json:"label"`
	SourceAuthID string `json:"source_auth_id"`
	Disabled     bool   `json:"disabled,omitempty"`
}
type stopRule struct {
	Status int      `json:"status"`
	Match  []string `json:"match"`
	Action string   `json:"action"`
}
type authMetadata struct {
	Type                string     `json:"type"`
	SourceAuthID        string     `json:"source_auth_id"`
	RequestScopedErrors []stopRule `json:"request_scoped_errors"`
}
type authData struct {
	Provider, ID, FileName, Label, ProxyURL string
	Disabled                                bool
	StorageJSON                             []byte
	Metadata                                authMetadata
}
type modelInfo struct {
	ID, Object, OwnedBy, Type, DisplayName, Name, Description                       string
	SupportedGenerationMethods, SupportedInputModalities, SupportedOutputModalities []string
}
type executorRequest struct {
	AuthID, AuthProvider, Model, Format, SourceFormat, Alt string
	Stream                                                 bool
	Payload, OriginalRequest, StorageJSON                  []byte
	AuthMetadata                                           authMetadata
	Metadata                                               struct {
		PinnedAuthID string `json:"pinned_auth_id"`
	}
	HostCallbackID string `json:"host_callback_id,omitempty"`
}
type managementRequest struct {
	Method, Path   string
	Body           []byte
	HostCallbackID string `json:"host_callback_id,omitempty"`
}
type httpResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}
type webInlinePart struct {
	MIMEType  string `json:"mimeType"`
	MIMESnake string `json:"mime_type"`
	Data      string `json:"data"`
}

func (part webInlinePart) mimeType() string {
	if part.MIMEType != "" {
		return part.MIMEType
	}
	return part.MIMESnake
}
func webExecutionResult(body []byte, stream bool) interface{} {
	if stream {
		return struct {
			Headers http.Header                `json:"headers"`
			Chunks  []struct{ Payload []byte } `json:"chunks"`
		}{http.Header{"Content-Type": {"text/event-stream"}}, []struct{ Payload []byte }{{body}}}
	}
	return struct {
		Payload []byte
		Headers http.Header
	}{body, http.Header{"Content-Type": {"application/json"}}}
}
