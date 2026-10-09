package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Google Flow (flow.google.com) runs on the same Google account session as the
// Gemini web app: its batchexecute endpoint authenticates with the account's
// cookies, and Google rotates them on Flow responses just as it does on Gemini
// ones. Flow calls therefore go through the account's own jar in this plugin.
// A second holder of the same session would fork the rotation and leave one of
// the two with cookies Google has already retired.

const (
	flowOrigin       = "https://flow.google.com"
	flowBatchPath    = "/_/AiSandboxAngularFrontend/data/batchexecute"
	flowProjectsPath = "/projects"
	// flowUserAgent is the browser every Flow call presents. A generation call
	// presents the one its captcha token was issued to instead, because Flow
	// refuses a token whose browser does not match the caller.
	flowUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"
	flowPageAge   = 10 * time.Minute
)

var (
	flowBuildPattern = regexp.MustCompile(`boq_labs-ai-sandbox-frontend_[A-Za-z0-9_.-]+`)
	flowXSRFPattern  = regexp.MustCompile(`\["xsrf","([^"]+)"`)
	flowUUIDPattern  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

type flowPage struct {
	build, sessionID, xsrf string
	read                   time.Time
}

type flowSession struct {
	service   *service
	accountID string
	origin    string
	page      flowPage
	requestID int
}

func (service *service) newFlowSession(reference string) *flowSession {
	origin := flowOrigin
	if service.flowOriginOverride != "" {
		origin = service.flowOriginOverride
	}
	session := &flowSession{service: service, accountID: reference, origin: origin, requestID: 100000}
	service.flowMu.Lock()
	session.page = service.flowPages[reference]
	service.flowMu.Unlock()
	return session
}

func (service *service) rememberFlowPage(reference string, page flowPage) {
	if page.build == "" {
		return
	}
	service.flowMu.Lock()
	defer service.flowMu.Unlock()
	if service.flowPages == nil {
		service.flowPages = map[string]flowPage{}
	}
	service.flowPages[reference] = page
}

func (session *flowSession) send(ctx context.Context, method, target string, body []byte, header http.Header) ([]byte, error) {
	if header == nil {
		header = http.Header{}
	}
	if header.Get("User-Agent") == "" {
		header.Set("User-Agent", flowUserAgent)
	}
	response, err := session.service.exchange(ctx, session.accountID, method, target, body, header)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == 401 || response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, failure(401, "flow_unauthenticated")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &publicError{Code: "flow_upstream_status", Message: fmt.Sprintf("flow_upstream_status: HTTP %d", response.StatusCode), HTTPStatus: 502}
	}
	return response.Body, nil
}

func (session *flowSession) bootstrap(ctx context.Context, sourcePath string) error {
	page, err := session.send(ctx, http.MethodGet, session.origin+sourcePath, nil, http.Header{
		"Accept":  {"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
		"Referer": {session.origin + "/"},
	})
	if err != nil {
		return err
	}
	text := string(page)
	build := flowBuildPattern.FindString(text)
	sessionID, found := bootstrapValue(bootstrapSessionPattern, text)
	if build == "" || !found {
		return failure(502, "flow_bootstrap_failed")
	}
	xsrf, _ := bootstrapValue(bootstrapXSRFPattern, text)
	session.page = flowPage{build: build, sessionID: sessionID, xsrf: xsrf, read: time.Now()}
	return nil
}

// rpc calls one Flow RPC from the page at sourcePath. userAgent, when set,
// replaces the default browser for this call only.
func (session *flowSession) rpc(ctx context.Context, rpcID string, args any, sourcePath, userAgent string) (any, error) {
	if session.page.build == "" || time.Since(session.page.read) > flowPageAge {
		if err := session.bootstrap(ctx, sourcePath); err != nil {
			return nil, err
		}
	}
	encodedArgs, err := json.Marshal(args)
	if err != nil {
		return nil, failure(400, "flow_request_invalid")
	}
	envelope, err := json.Marshal([]any{[]any{[]any{rpcID, string(encodedArgs), nil, "generic"}}})
	if err != nil {
		return nil, failure(400, "flow_request_invalid")
	}
	for attempt := 0; ; attempt++ {
		query := url.Values{
			"rpcids":      {rpcID},
			"source-path": {sourcePath},
			"bl":          {session.page.build},
			"f.sid":       {session.page.sessionID},
			"hl":          {"en"},
			"_reqid":      {strconv.Itoa(session.requestID)},
			"rt":          {"c"},
		}
		session.requestID += 100000
		form := url.Values{"f.req": {string(envelope)}}
		if session.page.xsrf != "" {
			form.Set("at", session.page.xsrf)
		}
		header := http.Header{
			"Accept":          {"*/*"},
			"Accept-Language": {"en-US,en;q=0.9"},
			"Content-Type":    {"application/x-www-form-urlencoded;charset=UTF-8"},
			"Origin":          {session.origin},
			"Referer":         {session.origin + sourcePath},
			"X-Same-Domain":   {"1"},
			"Sec-Fetch-Dest":  {"empty"},
			"Sec-Fetch-Mode":  {"cors"},
			"Sec-Fetch-Site":  {"same-origin"},
		}
		if userAgent != "" {
			header.Set("User-Agent", userAgent)
		}
		raw, err := session.send(ctx, http.MethodPost, session.origin+flowBatchPath+"?"+query.Encode(), []byte(form.Encode()), header)
		if err != nil {
			return nil, err
		}
		// A stale XSRF token is refused before the RPC runs, and the refusal
		// carries the token the page should have sent, so one resend is safe.
		if match := flowXSRFPattern.FindSubmatch(raw); match != nil && attempt == 0 && string(match[1]) != session.page.xsrf {
			session.page.xsrf = string(match[1])
			continue
		}
		return decodeFlowRPC(raw, rpcID)
	}
}

func decodeFlowRPC(raw []byte, rpcID string) (any, error) {
	entries, err := batchFrames(raw)
	if err != nil {
		return nil, failure(502, "flow_response_invalid")
	}
	for _, frame := range entries {
		switch jsonField(frame, 0) {
		case "er":
			code := jsonField(frame, 5)
			if code == nil {
				code = jsonField(frame, 6)
			}
			return nil, flowRejection(fmt.Sprint(code))
		case "wrb.fr":
			if jsonField(frame, 1) != rpcID {
				continue
			}
			detail := jsonField(frame, 5)
			if code := flowErrorCode(detail); code != "" {
				return nil, flowRejection(code)
			}
			encoded, ok := jsonField(frame, 2).(string)
			if !ok {
				if status, numbered := jsonInteger(jsonField(detail, 0)); numbered {
					return nil, flowRejection("RPC_CODE_" + strconv.Itoa(status))
				}
				return nil, failure(502, "flow_response_invalid")
			}
			var decoded any
			if json.Unmarshal([]byte(encoded), &decoded) != nil {
				return nil, failure(502, "flow_response_invalid")
			}
			return decoded, nil
		}
	}
	return nil, failure(502, "flow_response_invalid")
}

// flowErrorCode searches the whole error detail because Flow nests the code at
// a different depth per RPC.
func flowErrorCode(value any) string {
	switch typed := value.(type) {
	case string:
		upper := strings.ToUpper(typed)
		if strings.HasPrefix(upper, "PUBLIC_ERROR_") || strings.HasPrefix(upper, "ERROR_") || strings.Contains(upper, "MODEL_ACCESS_DENIED") || strings.Contains(upper, "RECAPTCHA") {
			return typed
		}
	case []any:
		for _, item := range typed {
			if code := flowErrorCode(item); code != "" {
				return code
			}
		}
	case map[string]any:
		for _, item := range typed {
			if code := flowErrorCode(item); code != "" {
				return code
			}
		}
	}
	return ""
}

// flowRejection maps Flow's refusal onto the status the host acts on. A token
// Flow judged unusual is retried with a fresh one; a refused prompt is the
// caller's to change; a model the plan does not include stays refused.
func flowRejection(code string) error {
	upper := strings.ToUpper(code)
	rejection := func(status int, name string) error {
		return &publicError{Code: name, Message: name + ": " + code, HTTPStatus: status}
	}
	switch {
	case strings.Contains(upper, "UNUSUAL_ACTIVITY") || strings.Contains(upper, "RECAPTCHA"):
		return rejection(503, "flow_captcha_rejected")
	case strings.Contains(upper, "MODEL_ACCESS_DENIED"):
		return rejection(403, "flow_model_access_denied")
	case upper == "401" || upper == "RPC_CODE_16":
		return failure(401, "flow_unauthenticated")
	case strings.Contains(upper, "QUOTA") || strings.Contains(upper, "CREDIT") || strings.Contains(upper, "RESOURCE_EXHAUSTED") || upper == "RPC_CODE_8":
		return rejection(429, "flow_quota_exhausted")
	case strings.Contains(upper, "UNSAFE") || strings.Contains(upper, "FILTER") || strings.Contains(upper, "SAFETY") || strings.Contains(upper, "POLICY") || upper == "RPC_CODE_3":
		return rejection(400, "flow_request_rejected")
	default:
		return rejection(502, "flow_rpc_rejected")
	}
}

func flowFindUUID(value any, excluded string) string {
	switch typed := value.(type) {
	case string:
		if flowUUIDPattern.MatchString(typed) && !strings.EqualFold(typed, excluded) {
			return typed
		}
	case []any:
		for _, item := range typed {
			if found := flowFindUUID(item, excluded); found != "" {
				return found
			}
		}
	}
	return ""
}

func flowFindURL(value any, kind string) string {
	switch typed := value.(type) {
	case string:
		if strings.HasPrefix(typed, "https://") && strings.Contains(typed, "/"+kind+"/") {
			return typed
		}
	case []any:
		for _, item := range typed {
			if found := flowFindURL(item, kind); found != "" {
				return found
			}
		}
	}
	return ""
}
