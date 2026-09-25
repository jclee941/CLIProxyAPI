package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// This file speaks the Gemini web account protocol directly. Discovery,
// generation and usage all run through it, so nothing here is delegated.
//
// The wire format is Google's batchexecute: a bootstrap page carries the XSRF
// token and build id, and every call is a length-prefixed frame envelope rather
// than plain JSON. None of it is documented, so the shapes here mirror the
// working Python implementation field for field.

// webModelHeader selects which capability fields the account RPC returns. The
// value is opaque and is sent verbatim.
const webModelHeader = "[1,null,null,null,null,null,null,null,[4,5,6,8],null,null,null,null,null,null,null]"

const webOrigin = "https://gemini.google.com"

// accountCapabilityRPC lists the models the signed-in account may use.
const accountCapabilityRPC = "otAQ7b"

var (
	bootstrapXSRFPattern    = regexp.MustCompile(`"SNlM0e"\s*:\s*("(?:[^"\\]|\\.)*")`)
	bootstrapBuildPattern   = regexp.MustCompile(`"cfb2h"\s*:\s*("(?:[^"\\]|\\.)*")`)
	bootstrapSessionPattern = regexp.MustCompile(`"FdrFJe"\s*:\s*("(?:[^"\\]|\\.)*")`)
)

type webCredential struct {
	Cookie   string `json:"cookie"`
	AuthUser int    `json:"auth_user"`
}

// decodeWebCredential reads the cookie the session token carries. The token is an
// encoding, not a secret box: the same value is what the sidecar decodes.
func decodeWebCredential(token sessionToken) (webCredential, error) {
	const prefix = "gemini-web:v1:"
	if !strings.HasPrefix(token.value, prefix) {
		return webCredential{}, failure(400, "invalid_credential")
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(token.value, prefix))
	if err != nil {
		return webCredential{}, failure(400, "invalid_credential")
	}
	var credential webCredential
	if json.Unmarshal(payload, &credential) != nil || credential.Cookie == "" {
		return webCredential{}, failure(400, "invalid_credential")
	}
	return credential, nil
}

type webSession struct {
	client          *http.Client
	origin          string
	cookie          string
	sapisid         string
	prefix          string
	xsrf            string
	build           string
	sessionID       string
	requestID       int
	generationFrame func([]byte) error
	rotated         bool
	onRotate        func(string)
	onCut           func(map[string]any)
	uploadOrigin    string
}

func newWebSession(client *http.Client, credential webCredential, origin string) *webSession {
	if origin == "" {
		origin = webOrigin
	}
	session := &webSession{client: client, origin: origin, cookie: credential.Cookie, requestID: 100000, uploadOrigin: webUploadOrigin}
	for _, pair := range strings.Split(credential.Cookie, ";") {
		name, value, found := strings.Cut(strings.TrimSpace(pair), "=")
		if found && name == "SAPISID" {
			session.sapisid = value
		}
	}
	if credential.AuthUser > 0 {
		session.prefix = "/u/" + strconv.Itoa(credential.AuthUser)
	}
	return session
}

// headers reproduce what the web client sends. The SAPISIDHASH authorization is
// derived per request from the cookie and the current second.
func (session *webSession) headers(now time.Time) http.Header {
	headers := http.Header{
		"Cookie":                    {session.cookie},
		"Origin":                    {session.origin},
		"Referer":                   {session.origin + session.prefix + "/app"},
		"X-Same-Domain":             {"1"},
		"User-Agent":                {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"},
		"Content-Type":              {"application/x-www-form-urlencoded;charset=UTF-8"},
		"x-goog-ext-525001261-jspb": {webModelHeader},
		"x-goog-ext-73010989-jspb":  {"[0]"},
	}
	if session.prefix != "" {
		headers.Set("X-Goog-AuthUser", strings.TrimPrefix(session.prefix, "/u/"))
	}
	if session.sapisid != "" {
		timestamp := now.Unix()
		digest := sha1.Sum([]byte(fmt.Sprintf("%d %s %s", timestamp, session.sapisid, session.origin)))
		headers.Set("Authorization", fmt.Sprintf("SAPISIDHASH %d_%x", timestamp, digest))
	}
	return headers
}

func (session *webSession) do(ctx context.Context, path string, body []byte, overrides http.Header) ([]byte, error) {
	method := http.MethodGet
	var reader io.Reader
	if body != nil {
		method, reader = http.MethodPost, strings.NewReader(string(body))
	}
	request, err := http.NewRequestWithContext(ctx, method, session.origin+path, reader)
	if err != nil {
		return nil, failure(400, "web_request_invalid")
	}
	request.Header = session.headers(time.Now())
	for name, values := range overrides {
		request.Header[name] = values
	}
	response, err := session.client.Do(request)
	if err != nil {
		return nil, failure(502, "web_transport_failed")
	}
	session.absorb(response)
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()
	// A redirect means the sign-in page, and 401 says so outright. Both have to
	// arrive as an authentication failure or maintenance never reaches the
	// branch that recaptures the credential.
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, &AuthenticationFailure{}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, failure(response.StatusCode, "web_upstream_status")
	}
	if session.generationFrame != nil && strings.Contains(path, webGeneratePath) {
		started := time.Now()
		raw, streamErr := readContinuationStream(response.Body, session.generationFrame)
		var cut *webStreamCut
		if errors.As(streamErr, &cut) && session.onCut != nil {
			session.onCut(streamCutFields(response, cut, time.Since(started)))
		}
		return raw, streamErr
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 32*1024*1024))
	if err != nil {
		return nil, failure(502, "web_response_failed")
	}
	return raw, nil
}

// streamCutFields describes a cut generation stream, which is the one question
// the failure alone cannot answer: a body short of the length it declared was
// truncated on the way, a chunked body missing its terminator was abandoned at
// the far end, and the transport error names the mechanism.
//
// The keys are the host formatter's own. Any other name reaches logrus and is
// dropped before it is written, so a more descriptive one would record nothing
// at all. Response headers are named one by one because a response also carries
// credentials, and none of these do.
func streamCutFields(response *http.Response, cut *webStreamCut, elapsed time.Duration) map[string]any {
	transport := fmt.Sprintf("%s %v", response.Proto, response.TransferEncoding)
	for _, name := range []string{"Content-Encoding", "Server", "Via", "Alt-Svc"} {
		if value := response.Header.Get(name); value != "" {
			transport += fmt.Sprintf(" %s=%s", strings.ToLower(name), value)
		}
	}
	return map[string]any{
		"provider": provider,
		"state":    "generation_stream_cut",
		"reason":   fmt.Sprint(cut.Cause),
		"error":    cut.Code,
		"budget": fmt.Sprintf("%d of %d bytes in %d frames after %s",
			len(cut.Delivered), response.ContentLength,
			bytes.Count(cut.Delivered, []byte{'\n'}), elapsed.Round(time.Millisecond)),
		"remote_transport": transport,
	}
}

// bootstrap reads the XSRF token and build id the RPC endpoint requires. They are
// embedded in the application page rather than served by an API.
// absorb takes the cookies Google hands back on an ordinary response. It rotates
// the session cookie opportunistically, not only through the rotation endpoint,
// so a jar that ignores these goes stale while upstream has already moved on.
func (session *webSession) absorb(response *http.Response) {
	updates := map[string]string{}
	for _, cookie := range response.Cookies() {
		domain := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")
		// SIDCC comes back without the Secure attribute while its __Secure- twins
		// carry it. Dropping the plain one leaves a half-rotated jar that Google
		// rejects, so the domain is the boundary here, not the flag.
		if cookie.Path != "/" || !webRotatableDomains[domain] || cookie.Value == "" {
			continue
		}
		updates[cookie.Name] = cookie.Value
	}
	if len(updates) == 0 {
		return
	}
	merged := webMergeCookies(session.cookie, updates)
	if merged == session.cookie {
		return
	}
	session.cookie, session.rotated = merged, true
	// A generation runs for minutes and Google rotates during it, so the jar is
	// stored the moment it changes. Waiting until the call returns loses the
	// rotation whenever the process goes down mid-turn.
	if session.onRotate != nil {
		session.onRotate(merged)
	}
	for _, pair := range strings.Split(merged, ";") {
		name, value, found := strings.Cut(strings.TrimSpace(pair), "=")
		if found && name == "SAPISID" {
			session.sapisid = value
		}
	}
}

func (session *webSession) bootstrap(ctx context.Context) error {
	page, err := session.do(ctx, session.prefix+"/app", nil, nil)
	if err != nil {
		return err
	}
	text := string(page)
	xsrf, okXSRF := bootstrapValue(bootstrapXSRFPattern, text)
	build, okBuild := bootstrapValue(bootstrapBuildPattern, text)
	if !okXSRF || !okBuild {
		return failure(502, "web_bootstrap_failed")
	}
	session.xsrf, session.build = xsrf, build
	if sessionID, ok := bootstrapValue(bootstrapSessionPattern, text); ok {
		session.sessionID = sessionID
	}
	return nil
}

func bootstrapValue(pattern *regexp.Regexp, page string) (string, bool) {
	match := pattern.FindStringSubmatch(page)
	if match == nil {
		return "", false
	}
	var value string
	if json.Unmarshal([]byte(match[1]), &value) != nil || value == "" {
		return "", false
	}
	return value, true
}

func (session *webSession) rpc(ctx context.Context, rpcID string, args any) (any, error) {
	if session.xsrf == "" {
		if err := session.bootstrap(ctx); err != nil {
			return nil, err
		}
	}
	encodedArgs, err := json.Marshal(args)
	if err != nil {
		return nil, failure(400, "web_request_invalid")
	}
	envelope, err := json.Marshal([]any{[]any{[]any{rpcID, string(encodedArgs), nil, "generic"}}})
	if err != nil {
		return nil, failure(400, "web_request_invalid")
	}
	query := url.Values{
		"rpcids":      {rpcID},
		"source-path": {session.prefix + "/app"},
		"bl":          {session.build},
		"hl":          {"en"},
		"_reqid":      {strconv.Itoa(session.requestID)},
		"rt":          {"c"},
	}
	if session.sessionID != "" {
		query.Set("f.sid", session.sessionID)
	}
	session.requestID += 100000
	body := url.Values{"f.req": {string(envelope)}, "at": {session.xsrf}}.Encode()
	raw, err := session.do(ctx, session.prefix+"/_/BardChatUi/data/batchexecute?"+query.Encode(), []byte(body), nil)
	if err != nil {
		return nil, err
	}
	return decodeRPCFrames(raw, rpcID)
}

// decodeRPCFrames unwraps the batchexecute envelope: an anti-JSON-hijacking
// prefix, then repeating length-and-payload pairs where the length counts UTF-16
// code units rather than bytes.
func decodeRPCFrames(raw []byte, rpcID string) (any, error) {
	remaining := strings.TrimLeft(strings.TrimPrefix(string(raw), ")]}'"), " \t\r\n")
	var results []string
	for remaining != "" {
		sizeLine, rest, found := strings.Cut(remaining, "\n")
		if !found {
			return nil, failure(502, "web_response_invalid")
		}
		size, err := strconv.Atoi(strings.TrimSpace(sizeLine))
		if err != nil {
			return nil, failure(502, "web_response_invalid")
		}
		frameData, tail, _ := strings.Cut(rest, "\n")
		if units := len(utf16.Encode([]rune(frameData))); size != units && size != units+2 {
			return nil, failure(502, "web_response_invalid")
		}
		var frames []any
		if json.Unmarshal([]byte(frameData), &frames) != nil {
			return nil, failure(502, "web_response_invalid")
		}
		remaining = strings.TrimLeft(tail, " \t\r\n")
		for _, frame := range frames {
			if jsonField(frame, 0) != "wrb.fr" || jsonField(frame, 1) != rpcID {
				continue
			}
			if jsonField(frame, 5, 0) != nil {
				return nil, failure(403, "web_rpc_denied")
			}
			encoded, ok := jsonField(frame, 2).(string)
			if !ok {
				return nil, failure(502, "web_response_invalid")
			}
			results = append(results, encoded)
		}
	}
	if len(results) != 1 {
		return nil, failure(502, "web_response_invalid")
	}
	var decoded any
	if json.Unmarshal([]byte(results[0]), &decoded) != nil {
		return nil, failure(502, "web_response_invalid")
	}
	return decoded, nil
}

// jsonField walks positional indexes, which is how every value in this protocol
// is addressed; a missing or non-list step yields nil rather than an error.
func jsonField(value any, path ...int) any {
	current := value
	for _, index := range path {
		list, ok := current.([]any)
		if !ok || index >= len(list) || index < 0 {
			return nil
		}
		current = list[index]
	}
	return current
}

// jsonNumber reads a decoded number without forcing it to an integer, and
// reports absence as absence: a quota figure the account does not publish is
// missing rather than zero, which reads as spent.
func jsonNumber(value any) *float64 {
	number, ok := value.(float64)
	if !ok {
		return nil
	}
	return &number
}

func jsonInteger(value any) (int, bool) {
	number, ok := value.(float64)
	if !ok || number != float64(int(number)) {
		return 0, false
	}
	return int(number), true
}

// webAccount is what the capability RPC reports: the models the account may use
// and the capacity flags that generation has to echo back.
type webAccount struct {
	Capabilities  []capability
	CapacityFlags []int
	// Features are the product features the web app gates its UI on; its /veo
	// page, for one, only opens for an account whose list carries 140.
	Features []int
}

// webCapabilities reports the models the account may use, in the same shape the
// sidecar reports them, so the two can be compared before the sidecar is retired.
func (session *webSession) webCapabilities(ctx context.Context) (webAccount, error) {
	body, err := session.rpc(ctx, accountCapabilityRPC, []any{})
	if err != nil {
		return webAccount{}, err
	}
	// The account status is read before the capability list, because a body that
	// reports one of these carries no list to read: asking for the list first
	// reported a signed-out account as an unreadable response, which named
	// neither the cause nor anything an operator could act on.
	if status, present := jsonInteger(jsonField(body, 14)); present {
		if status == 1016 {
			return webAccount{}, failure(401, "web_unauthenticated")
		}
		if status != 1000 {
			return webAccount{}, failure(409, "web_account_unavailable")
		}
	}
	rows, ok := jsonField(body, 15).([]any)
	if !ok {
		return webAccount{}, failure(502, "web_response_invalid")
	}
	account := webAccount{Capabilities: make([]capability, 0, len(rows))}
	for _, row := range rows {
		identifier, okID := jsonField(row, 0).(string)
		display, okDisplay := jsonField(row, 11).(string)
		mode, okMode := jsonInteger(jsonField(row, 17))
		if !okID || !okDisplay || !okMode || identifier == "" || display == "" {
			return webAccount{}, failure(502, "web_response_invalid")
		}
		account.Capabilities = append(account.Capabilities, capability{CapabilityID: identifier, DisplayName: display, Mode: mode})
	}
	if flags, present := jsonField(body, 16).([]any); present {
		for _, flag := range flags {
			if value, ok := jsonInteger(flag); ok {
				account.CapacityFlags = append(account.CapacityFlags, value)
			}
		}
	}
	if features, present := jsonField(body, 17).([]any); present {
		for _, feature := range features {
			if value, ok := jsonInteger(feature); ok {
				account.Features = append(account.Features, value)
			}
		}
	}
	return account, nil
}
