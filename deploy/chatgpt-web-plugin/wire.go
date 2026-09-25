package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

const (
	provider     = "chatgpt-web"
	authProvider = "codex"
	usageURL     = "https://chatgpt.com/backend-api/wham/usage"

	resourcePath        = "/v0/resource/plugins/chatgpt-web/index"
	defaultDashboard    = "/CLIProxyAPI/plugins/chatgpt-web/index.html"
	conversationInitURL = "https://chatgpt.com/backend-api/conversation/init"
	browserUserAgent    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.7499.147 Safari/537.36 Edg/143.0.3650.96"
)

const (
	// imagesModelType marks the model as callable through /v1/images/generations.
	imagesModelType = "openai-image"

	imagesDefaultFormat = "png"

	// imagesMaxEventBytes bounds one SSE line; a 1024x1024 base64 PNG is about 1 MB.
	imagesMaxEventBytes = 32 * 1024 * 1024
)

type modelInfo struct {
	ID                        string
	Object                    string
	OwnedBy                   string
	Type                      string
	DisplayName               string
	Name                      string
	Description               string
	SupportedInputModalities  []string
	SupportedOutputModalities []string
}

type modelResponse struct {
	Provider string
	Models   []modelInfo
}

type modelRouteRequest struct {
	SourceFormat   string
	RequestedModel string
	Stream         bool
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type modelRouteResponse struct {
	Handled    bool
	TargetKind string
	Target     string
	Reason     string
}

type executorRequest struct {
	AuthID          string
	Model           string
	Format          string
	SourceFormat    string
	Stream          bool
	Payload         []byte
	OriginalRequest []byte
	HostCallbackID  string `json:"host_callback_id,omitempty"`
	StreamID        string `json:"stream_id,omitempty"`
}

type executorResponse struct {
	Payload []byte
	Headers http.Header
}

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *publicError    `json:"error,omitempty"`
}

type publicError struct {
	HTTPStatus int    `json:"http_status"`
	Code       string `json:"code"`
	// Message is what the host surfaces to the client; without it every plugin
	// error reaches the caller as the opaque "plugin call failed".
	Message string `json:"message,omitempty"`
}

func (err *publicError) Error() string {
	return fmt.Sprintf("%d %s", err.HTTPStatus, err.Code)
}

func failure(status int, code string) *publicError {
	return &publicError{HTTPStatus: status, Code: code, Message: code}
}

func strictJSON(raw []byte, target interface{}) error {
	if !json.Valid(raw) {
		return failure(400, "invalid_json")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return failure(400, "invalid_json_schema")
	}
	return nil
}

type managementRequest struct {
	Method         string          `json:"Method"`
	Path           string          `json:"Path"`
	Body           []byte          `json:"Body"`
	HostCallbackID string          `json:"host_callback_id,omitempty"`
	Query          json.RawMessage `json:"Query,omitempty"`
}

type callbackRequest struct {
	HostCallbackID string `json:"host_callback_id,omitempty"`
	AuthIndex      string `json:"auth_index,omitempty"`
}

type hostEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AuthIndex string `json:"auth_index"`
	Provider  string `json:"provider"`
	Disabled  bool   `json:"disabled"`
}

type quotaSubscription struct {
	Plan     string `json:"plan,omitempty"`
	TierName string `json:"tierName,omitempty"`
	TierID   string `json:"tierId,omitempty"`
}

type quotaBucket struct {
	Window            string  `json:"window,omitempty"`
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime,omitempty"`
	Description       string  `json:"description,omitempty"`
}

type quotaGroup struct {
	DisplayName string        `json:"displayName,omitempty"`
	Buckets     []quotaBucket `json:"buckets,omitempty"`
}

type quotaFetchResponse struct {
	Subscription *quotaSubscription `json:"subscription,omitempty"`
	Groups       []quotaGroup       `json:"groups,omitempty"`
}

type quotaDescription struct {
	SupportedProviders []string `json:"supported_providers"`
	DisplayName        string   `json:"display_name"`
	SupportsReset      bool     `json:"supports_reset"`
}
