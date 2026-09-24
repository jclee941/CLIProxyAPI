package pluginapi

import (
	"context"
	"net/http"
	"net/url"
)

const (
	// FrontendHTTPMaxBodyBytes bounds buffered frontend request and response bodies.
	// Upload larger objects in multiple requests. This capability does not stream.
	FrontendHTTPMaxBodyBytes = 128 << 20
	// FrontendHTTPMaxMessageBytes bounds the JSON RPC message, including base64
	// body expansion and headers, below the native plugin's 192 MiB ABI ceiling.
	FrontendHTTPMaxMessageBytes = 192 << 20
)

// FrontendHTTP declares authenticated routes using frontend_http.register.
// Native plugins advertise the additive "frontend_http": true capability.
// The C ABI and schema version remain unchanged.
type FrontendHTTP interface {
	RegisterFrontendHTTP(context.Context, FrontendHTTPRegistrationRequest) (FrontendHTTPRegistrationResponse, error)
}

type FrontendHTTPRegistrationRequest struct {
	Plugin Metadata
}

type FrontendHTTPRegistrationResponse struct {
	Routes []FrontendHTTPRoute
}

// FrontendHTTPAuthMode selects authentication for one frontend route.
type FrontendHTTPAuthMode string

const (
	// FrontendHTTPAuthCore uses core API-key authentication (the default).
	FrontendHTTPAuthCore FrontendHTTPAuthMode = ""
	// FrontendHTTPAuthScoped authenticates an opaque bearer capability belonging
	// to this route's plugin, recovering a previously core-derived caller scope.
	FrontendHTTPAuthScoped FrontendHTTPAuthMode = "scoped"
)

// FrontendHTTPRoute describes a frontend route, not a management or public resource route.
// Standard host routes always win. Overlapping plugin routes use plugin priority.
type FrontendHTTPRoute struct {
	// Method is GET, HEAD, POST, PUT, PATCH, or DELETE.
	Method string
	// Path is an absolute path with a literal first segment. Segments may be
	// literal, {name}, or {name}:action. Parameters match one nonempty URL-safe
	// ASCII segment (letters, digits, '.', '_', '~', '-'), excluding '.' and '..'.
	// Wildcards, encoded patterns, empty segments, and traversal are forbidden.
	Path string
	// AuthMode defaults to core authentication. Unknown modes are rejected.
	AuthMode FrontendHTTPAuthMode `json:",omitempty"`
	// Authenticator is required for scoped routes and receives no request body.
	// Native routes reuse frontend_auth.authenticate on the route owner's adapter;
	// they do not need or register the global frontend_auth_provider capability.
	// A failed scoped decision never falls back to core authentication.
	Authenticator FrontendAuthProvider `json:"-"`
	Handler       FrontendHTTPHandler  `json:"-"`
}

// FrontendHTTPHandler receives frontend_http.handle after route authentication.
type FrontendHTTPHandler interface {
	HandleFrontendHTTP(context.Context, FrontendHTTPRequest) (FrontendHTTPResponse, error)
}

type FrontendHTTPRequest struct {
	Method     string
	Path       string
	RawPath    string
	RawQuery   string
	RequestURI string
	// Scheme reflects the inbound transport, not client-supplied forwarded headers.
	Scheme        string
	Host          string
	ContentLength int64
	Headers       http.Header
	Query         url.Values
	Body          []byte
	// Params contains decoded path parameter values, without any :action suffix.
	Params map[string]string
	// CallerScope is a host-derived, irreversible namespace for the authenticated
	// principal, identical to execution metadata caller_scope. Scoped authentication
	// restores this already-derived scope without rehashing. The host never derives
	// it from raw request fields or plugin identity metadata.
	// Plugins must scope stored resources and upload sessions to this value.
	CallerScope string `json:"caller_scope"`
}

// FrontendHTTPResponse is transported without JSON/HTML body rewriting.
type FrontendHTTPResponse struct {
	// StatusCode is a final HTTP status (200-599). Zero defaults to 200.
	StatusCode int
	Headers    http.Header
	Body       []byte
}
