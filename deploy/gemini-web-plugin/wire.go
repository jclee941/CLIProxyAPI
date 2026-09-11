package main

import (
	"encoding/json"
	"net/http"
	"net/url"
)

const (
	provider     = "gemini-web"
	flashModel   = "gemini-web-flash-3.8"
	omniModel    = "gemini-web-omni"
	sidecarBase  = "http://gemini-web2api:8081"
	resourcePath = "/v0/resource/plugins/gemini-web/index"
	accountsPath = "/v0/management/plugins/gemini-web/accounts"
	refreshPath  = "/v0/management/plugins/gemini-web/refresh"
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

func (failure *publicError) Error() string { return failure.Message }

func failure(status int, code string) *publicError {
	return &publicError{Code: code, Message: code, HTTPStatus: status}
}

type storageRecord struct {
	Type            string `json:"type"`
	ID              string `json:"id"`
	Label           string `json:"label"`
	TokenRef        string `json:"token_ref"`
	Disabled        bool   `json:"disabled,omitempty"`
	SessionRevision uint64 `json:"session_revision,omitempty"`
}

type stopRule struct {
	Status int      `json:"status"`
	Match  []string `json:"match"`
	Action string   `json:"action"`
}

type authMetadata struct {
	Type                string     `json:"type"`
	TokenRef            string     `json:"token_ref"`
	RequestScopedErrors []stopRule `json:"request_scoped_errors"`
	SessionRevision     uint64     `json:"session_revision,omitempty"`
}

type authData struct {
	Provider    string
	ID          string
	FileName    string
	Label       string
	ProxyURL    string
	Disabled    bool
	StorageJSON []byte
	Metadata    authMetadata
}

type modelInfo struct {
	ID                         string
	Object                     string
	OwnedBy                    string
	Type                       string
	DisplayName                string
	Name                       string
	Description                string
	SupportedGenerationMethods []string
	SupportedInputModalities   []string
	SupportedOutputModalities  []string
}

type executorRequest struct {
	AuthID          string
	AuthProvider    string
	Model           string
	Format          string
	SourceFormat    string
	Stream          bool
	Alt             string
	Payload         []byte
	OriginalRequest []byte
	StorageJSON     []byte
	AuthMetadata    authMetadata
	Metadata        struct {
		PinnedAuthID string `json:"pinned_auth_id"`
	}
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type executorHTTPRequest struct {
	AuthID         string
	AuthProvider   string
	Method         string
	URL            string
	StorageJSON    []byte
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type managementRequest struct {
	Method         string
	Path           string
	Headers        http.Header
	Query          url.Values
	Body           []byte
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type httpResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

type hostEntry struct {
	ID        string `json:"id"`
	AuthIndex string `json:"auth_index"`
	Provider  string `json:"provider"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Disabled  bool   `json:"disabled"`
}

type callbackRequest struct {
	HostCallbackID string          `json:"host_callback_id,omitempty"`
	AuthIndex      string          `json:"auth_index,omitempty"`
	Name           string          `json:"name,omitempty"`
	JSON           json.RawMessage `json:"json,omitempty"`
}
