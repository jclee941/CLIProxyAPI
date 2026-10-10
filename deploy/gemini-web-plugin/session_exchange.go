package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// The session exchange lets an independent plugin make Flow and reCAPTCHA calls
// as one of this plugin's accounts without ever holding its cookies. The jar
// stays here: the cookie is attached to the outgoing request, Google's
// rotations are absorbed into the account's own encrypted record, and the
// response leaves with only the headers the caller needs. A second holder of
// the session would fork the rotation and strand one side on retired cookies.

const (
	sessionExchangeLimit     = 32 * 1024 * 1024
	sessionExchangeFlowHost  = "flow.google.com"
	sessionExchangeGoogle    = "www.google.com"
	sessionExchangeCaptcha   = "/recaptcha/"
	sessionExchangeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"
)

// sessionExchangeDenied are the headers the broker owns. The caller's copies are
// dropped rather than trusted: the cookie and account selector come from the
// credential, and authorization is never the caller's to supply.
var sessionExchangeDenied = []string{"Cookie", "Authorization", "Proxy-Authorization", "X-Goog-Authuser", "Host"}

// sessionExchangeUploadHeaders are the Scotty resumable-upload response headers
// a Flow upload needs to continue: where to send the bytes, the session state,
// the chunk size and the offset Google has accepted. Nothing else is passed.
var sessionExchangeUploadHeaders = []string{"X-Goog-Upload-URL", "X-Goog-Upload-Status", "X-Goog-Upload-Chunk-Granularity", "X-Goog-Upload-Size-Received"}

type sessionExchangeRequest struct {
	AuthID    string                   `json:"auth_id"`
	RequestID sessionExchangeRequestID `json:"request_id,omitempty"`
	Method    string                   `json:"method"`
	URL       string                   `json:"url"`
	Headers   http.Header              `json:"headers"`
	Body      []byte                   `json:"body"`
}

type sessionExchangeResult struct {
	StatusCode int         `json:"status_code"`
	Headers    http.Header `json:"headers"`
	Body       []byte      `json:"body"`
}

func sessionExchangeError(status int, name string) *publicError {
	return failure(status, "session_exchange_"+name)
}

// sessionExchangeTarget limits cookie-bearing requests to Flow, reCAPTCHA and
// passive Google sign-in returning to Flow; interactive login is not exposed.
func sessionExchangeTarget(raw string) (*url.URL, error) {
	if strings.ContainsAny(raw, "\\\r\n") {
		return nil, sessionExchangeError(400, "url_invalid")
	}
	target, err := url.Parse(raw)
	if err != nil || target.Scheme != "https" || target.User != nil || target.Opaque != "" || target.Host == "" {
		return nil, sessionExchangeError(403, "url_denied")
	}
	if port := target.Port(); port != "" && port != "443" {
		return nil, sessionExchangeError(403, "url_denied")
	}
	for _, segment := range strings.Split(target.Path, "/") {
		if segment == "." || segment == ".." {
			return nil, sessionExchangeError(403, "url_denied")
		}
	}
	switch target.Hostname() {
	case sessionExchangeFlowHost:
		// The broker owns the account prefix; a path that carries one would
		// address whichever account the caller named instead of this one.
		if strings.Contains(target.Path+"/", "/u/") {
			return nil, sessionExchangeError(403, "path_denied")
		}
	case sessionExchangeGoogle:
		if !strings.HasPrefix(target.Path, sessionExchangeCaptcha) {
			return nil, sessionExchangeError(403, "url_denied")
		}
	case "accounts.google.com":
		query := target.Query()
		if target.Path != "/ServiceLogin" || query.Get("passive") != "1209600" {
			return nil, sessionExchangeError(403, "url_denied")
		}
		for _, field := range []string{"continue", "followup"} {
			value := query.Get(field)
			if field == "followup" && value == "" {
				continue
			}
			next, err := url.Parse(value)
			if err != nil || next.Scheme != "https" || next.Host != sessionExchangeFlowHost || next.User != nil {
				return nil, sessionExchangeError(403, "url_denied")
			}
		}
	default:
		return nil, sessionExchangeError(403, "url_denied")
	}
	target.Fragment, target.RawFragment = "", ""
	return target, nil
}

// sessionExchangePrefixed puts the account prefix on a Flow path unless it
// already carries one.
func sessionExchangePrefixed(prefix, value string) string {
	if prefix == "" || !strings.HasPrefix(value, "/") || strings.Contains(value+"/", "/u/") {
		return value
	}
	return prefix + value
}

// sessionExchangeSourcePath rewrites only the source-path parameter and leaves
// every other pair byte-for-byte, because Flow may sign the rest of the query.
func sessionExchangeSourcePath(rawQuery, prefix string) string {
	if prefix == "" || rawQuery == "" {
		return rawQuery
	}
	pairs := strings.Split(rawQuery, "&")
	for index, pair := range pairs {
		name, value, found := strings.Cut(pair, "=")
		if key, err := url.QueryUnescape(name); !found || err != nil || key != "source-path" {
			continue
		}
		decoded, err := url.QueryUnescape(value)
		if err != nil {
			continue
		}
		pairs[index] = name + "=" + url.QueryEscape(sessionExchangePrefixed(prefix, decoded))
	}
	return strings.Join(pairs, "&")
}

func sessionExchangeHeaders(caller http.Header, prefix string, credential webCredential, cookie string) http.Header {
	headers := http.Header{}
	for name, values := range caller {
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if slices.Contains(sessionExchangeDenied, canonical) {
			continue
		}
		headers[canonical] = append(headers[canonical], values...)
	}
	if referer := headers.Get("Referer"); referer != "" {
		if parsed, err := url.Parse(referer); err == nil && parsed.Hostname() == sessionExchangeFlowHost {
			parsed.Path, parsed.RawPath = sessionExchangePrefixed(prefix, parsed.Path), ""
			headers.Set("Referer", parsed.String())
		}
	}
	if headers.Get("User-Agent") == "" {
		headers.Set("User-Agent", sessionExchangeUserAgent)
	}
	headers.Set("Cookie", cookie)
	headers.Set("X-Goog-AuthUser", strconv.Itoa(credential.AuthUser))
	return headers
}

// sessionExchangeFlowCookie adds the Flow host-only cookies to the root jar for
// a Flow request. They replace a same-named root cookie in place and otherwise
// append in a fixed order; no other request ever calls this.
func sessionExchangeFlowCookie(cookie string, flow map[string]string) string {
	for _, name := range webFlowCookieNames {
		if value, ok := flow[name]; ok {
			cookie = webMergeCookies(cookie, map[string]string{name: value})
		}
	}
	return cookie
}

// sessionExchange performs one allowlisted request as a configured account.
func (service *service) sessionExchange(ctx context.Context, request managementRequest) (interface{}, error) {
	var body sessionExchangeRequest
	if json.Unmarshal(request.Body, &body) != nil {
		return nil, sessionExchangeError(400, "request_invalid")
	}
	if body.Method != http.MethodGet && body.Method != http.MethodPost {
		return nil, sessionExchangeError(400, "method_invalid")
	}
	if !slices.Contains(service.settings().SessionExchangeAccounts, body.AuthID) {
		return nil, sessionExchangeError(403, "account_denied")
	}
	target, err := sessionExchangeTarget(body.URL)
	if err != nil {
		return nil, err
	}
	if target.Hostname() == "accounts.google.com" && body.Method != http.MethodGet {
		return nil, sessionExchangeError(403, "method_invalid")
	}
	record, enabled, err := service.findRecord(request.HostCallbackID, body.AuthID)
	if err != nil {
		if safeCredentialCode(err) == "account_not_found" {
			return nil, sessionExchangeError(404, "account_not_found")
		}
		return nil, sessionExchangeFailure(err)
	}
	if !enabled {
		return nil, sessionExchangeError(409, "account_disabled")
	}
	if !localReferencePattern.MatchString(record.TokenRef) {
		return nil, sessionExchangeError(400, "credential_invalid")
	}
	owned, finish, err := service.beginSessionExchange(ctx, sessionExchangeKey{AuthID: record.ID, RequestID: body.RequestID})
	if err != nil {
		return nil, err
	}
	defer finish()
	lease, err := service.accountLease(record)
	if err != nil {
		return nil, sessionExchangeFailure(err)
	}
	if !lease.guard.TryRLock() {
		return nil, sessionExchangeError(409, "busy")
	}
	defer lease.guard.RUnlock()
	// Borrowing the credential must not resolve or replay the Gemini turn.
	// The model snapshot already distinguishes recoverable submissions from
	// renewals, untracked intents and live credential fences.
	local, err := service.localModelSnapshot(record)
	if err != nil {
		return nil, sessionExchangeFailure(err)
	}
	if local.State == localHostPending {
		return nil, failure(409, "host_sync_pending")
	}
	credential, err := decodeWebCredential(sessionToken{local.Token})
	if err != nil {
		return nil, sessionExchangeFailure(err)
	}
	// Every absorbed rotation is written back to the account's own record the
	// moment it arrives, so the next exchange and the next Gemini turn both
	// start from the live jar.
	session := service.newSession(credential)
	service.trackJar(record.TokenRef, session)

	// Decided before the test origin override rewrites the host, so the Flow
	// cookies follow the requested origin and never the fixture's.
	flowTarget := target.Hostname() == sessionExchangeFlowHost
	flowUpload := flowTarget && strings.HasPrefix(target.Path, "/upload/v1/flow/upload/video/")
	if flowTarget {
		target.Path, target.RawPath = session.prefix+target.Path, ""
		target.RawQuery = sessionExchangeSourcePath(target.RawQuery, session.prefix)
	} else if target.Hostname() == "accounts.google.com" {
		query := target.Query()
		query.Set("authuser", strconv.Itoa(credential.AuthUser))
		target.RawQuery = query.Encode()
	}
	cookie := session.cookie
	if flowTarget {
		cookie = sessionExchangeFlowCookie(cookie, session.flowCookies)
	}
	headers := sessionExchangeHeaders(body.Headers, session.prefix, credential, cookie)
	if override := service.sessionExchangeOriginOverride; override != "" {
		origin, err := url.Parse(override)
		if err != nil {
			return nil, sessionExchangeError(500, "origin_invalid")
		}
		target.Scheme, target.Host = origin.Scheme, origin.Host
	}

	var reader io.Reader
	if len(body.Body) > 0 {
		reader = bytes.NewReader(body.Body)
	}
	outgoing, err := http.NewRequestWithContext(owned, body.Method, target.String(), reader)
	if err != nil {
		return nil, sessionExchangeError(400, "request_invalid")
	}
	outgoing.Header = headers
	// A redirect would carry the session to wherever Google points it, so the
	// answer is returned as received and the caller decides.
	client := *service.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(outgoing)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(owned.Err(), context.Canceled) {
			return nil, sessionExchangeError(499, "cancelled")
		}
		return nil, sessionExchangeError(502, "transport_failed")
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()
	session.absorb(response)
	if flowTarget {
		session.absorbFlow(response)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, sessionExchangeLimit+1))
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(owned.Err(), context.Canceled) {
			return nil, sessionExchangeError(499, "cancelled")
		}
		return nil, sessionExchangeError(502, "response_failed")
	}
	if len(raw) > sessionExchangeLimit {
		return nil, sessionExchangeError(502, "response_too_large")
	}
	result := sessionExchangeResult{StatusCode: response.StatusCode, Headers: http.Header{}, Body: raw}
	for _, name := range []string{"Content-Type", "Location"} {
		if value := response.Header.Get(name); value != "" {
			result.Headers.Set(name, value)
		}
	}
	if flowUpload {
		for _, name := range sessionExchangeUploadHeaders {
			if value := response.Header.Get(name); value != "" {
				result.Headers.Set(name, value)
			}
		}
	}
	return result, nil
}

// sessionExchangeFailure passes along a failure the plugin already named and
// reduces anything else to a generic code, so no detail of the credential or
// the upstream can reach the caller.
func sessionExchangeFailure(err error) error {
	var public *publicError
	if errors.As(err, &public) {
		return public
	}
	return sessionExchangeError(500, "failed")
}
