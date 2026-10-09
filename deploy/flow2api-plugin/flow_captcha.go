package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const flowRecaptchaKey = "6LdsFiUsAAAAAIjVDZcuLhaHiDn5nnHVXVRQGeMV"

var (
	recaptchaVersionPattern = regexp.MustCompile(`releases/([^/]+)/`)
	recaptchaAnchorPattern  = regexp.MustCompile(`id="recaptcha-token" value="([^"]+)"`)
	recaptchaTokenPattern   = regexp.MustCompile(`\["rresp","([^"]+)"`)
)

// nativeToken asks Google for a token the way the page's own script does,
// anchor then reload, over the account's session. No browser runs, so the token
// carries no browser evidence, and whether its score passes is Flow's call.
func (session *flowSession) nativeToken(ctx context.Context, origin, action string) (flowCaptchaToken, error) {
	script, err := session.send(ctx, http.MethodGet, origin+"/recaptcha/enterprise.js?render="+flowRecaptchaKey, nil, http.Header{"Referer": {flowOrigin + "/"}})
	if err != nil {
		return flowCaptchaToken{}, err
	}
	version := recaptchaVersionPattern.FindSubmatch(script)
	if version == nil {
		return flowCaptchaToken{}, failure(502, "flow_recaptcha_unavailable")
	}
	co := strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(flowOrigin+":443")), "=", ".")
	anchorURL := origin + "/recaptcha/enterprise/anchor?" + url.Values{
		"ar": {"1"}, "k": {flowRecaptchaKey}, "co": {co}, "hl": {"en"}, "v": {string(version[1])},
		"size": {"invisible"}, "cb": {strings.ReplaceAll(strings.ToLower(flowID()), "-", "")[:12]},
	}.Encode()
	anchor, err := session.send(ctx, http.MethodGet, anchorURL, nil, http.Header{"Referer": {flowOrigin + "/"}})
	if err != nil {
		return flowCaptchaToken{}, err
	}
	challenge := recaptchaAnchorPattern.FindSubmatch(anchor)
	if challenge == nil {
		return flowCaptchaToken{}, failure(502, "flow_recaptcha_unavailable")
	}
	form := url.Values{"v": {string(version[1])}, "reason": {"q"}, "c": {string(challenge[1])}, "k": {flowRecaptchaKey}, "co": {co}, "hl": {"en"}, "size": {"invisible"}, "sa": {action}}
	reply, err := session.send(ctx, http.MethodPost, origin+"/recaptcha/enterprise/reload?k="+flowRecaptchaKey, []byte(form.Encode()), http.Header{
		"Content-Type": {"application/x-www-form-urlencoded"},
		"Referer":      {anchorURL},
	})
	if err != nil {
		return flowCaptchaToken{}, err
	}
	token := recaptchaTokenPattern.FindSubmatch(reply)
	if token == nil {
		return flowCaptchaToken{}, failure(502, "flow_recaptcha_unavailable")
	}
	return flowCaptchaToken{value: string(token[1]), userAgent: flowUserAgent}, nil
}

// The solve is credential acquisition, the one wait this plugin may bound by a
// clock. These are variables so a test does not sit through the polling.
var (
	flowCaptchaPoll     = 3 * time.Second
	flowCaptchaAttempts = 40
	flowCaptchaCall     = 30 * time.Second
)

type flowCaptchaToken struct {
	value, userAgent string
}

type flowSolver struct {
	base, task string
	minScore   float64
}

func flowSolverFor(settings pluginConfig) flowSolver {
	solver := flowSolver{base: "https://api.yescaptcha.com", task: "RecaptchaV3TaskProxylessM1S9"}
	if settings.FlowCaptchaProvider == "capsolver" {
		solver = flowSolver{base: "https://api.capsolver.com", task: "ReCaptchaV3EnterpriseTaskProxyLess"}
	}
	if settings.FlowCaptchaBaseURL != "" {
		solver.base = strings.TrimRight(settings.FlowCaptchaBaseURL, "/")
	}
	if settings.FlowCaptchaTask != "" {
		solver.task = settings.FlowCaptchaTask
	}
	// YesCaptcha's S7/S9 task types only issue tokens above that score.
	switch {
	case strings.HasSuffix(solver.task, "S9"):
		solver.minScore = 0.9
	case strings.HasSuffix(solver.task, "S7"):
		solver.minScore = 0.7
	}
	return solver
}

// flowCaptcha obtains one token for action on pageURL. The browser the solver
// used comes back with it, and the call that spends the token presents it.
func (service *service) flowCaptcha(ctx context.Context, action, pageURL string) (flowCaptchaToken, error) {
	settings := service.settings()
	if settings.FlowCaptchaKey == "" {
		return flowCaptchaToken{}, failure(503, "flow_captcha_unconfigured")
	}
	solver := flowSolverFor(settings)
	task := map[string]any{"type": solver.task, "websiteURL": pageURL, "websiteKey": flowRecaptchaKey, "pageAction": action, "userAgent": flowUserAgent}
	if solver.minScore > 0 {
		task["minScore"] = solver.minScore
	}
	var created struct {
		ErrorID   int    `json:"errorId"`
		ErrorCode string `json:"errorCode"`
		TaskID    string `json:"taskId"`
	}
	for attempt := 0; ; attempt++ {
		if err := service.flowSolverCall(ctx, solver.base+"/createTask", map[string]any{"clientKey": settings.FlowCaptchaKey, "task": task}, &created); err != nil {
			return flowCaptchaToken{}, err
		}
		// A busy solver is the one refusal that clears by waiting.
		if !strings.HasPrefix(created.ErrorCode, "ERROR_NO_SLOT_AVAILABLE") || attempt >= 2 {
			break
		}
		if err := service.waitFlow(ctx, 2*flowCaptchaPoll); err != nil {
			return flowCaptchaToken{}, err
		}
	}
	if created.ErrorID != 0 || created.TaskID == "" {
		return flowCaptchaToken{}, flowCaptchaFailure(created.ErrorCode)
	}
	for attempt := 0; attempt < flowCaptchaAttempts; attempt++ {
		if err := service.waitFlow(ctx, flowCaptchaPoll); err != nil {
			return flowCaptchaToken{}, err
		}
		var result struct {
			ErrorID   int    `json:"errorId"`
			ErrorCode string `json:"errorCode"`
			Status    string `json:"status"`
			Solution  struct {
				Token     string `json:"gRecaptchaResponse"`
				UserAgent string `json:"userAgent"`
			} `json:"solution"`
		}
		if err := service.flowSolverCall(ctx, solver.base+"/getTaskResult", map[string]any{"clientKey": settings.FlowCaptchaKey, "taskId": created.TaskID}, &result); err != nil {
			return flowCaptchaToken{}, err
		}
		if result.ErrorID != 0 {
			return flowCaptchaToken{}, flowCaptchaFailure(result.ErrorCode)
		}
		if result.Status != "ready" {
			continue
		}
		if result.Solution.Token == "" {
			return flowCaptchaToken{}, flowCaptchaFailure("empty_solution")
		}
		userAgent := result.Solution.UserAgent
		if userAgent == "" {
			userAgent = flowUserAgent
		}
		return flowCaptchaToken{value: result.Solution.Token, userAgent: userAgent}, nil
	}
	return flowCaptchaToken{}, failure(503, "flow_captcha_timeout")
}

func (service *service) waitFlow(ctx context.Context, interval time.Duration) error {
	if service.flowWait != nil {
		return service.flowWait(ctx, interval)
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return failure(499, "flow_request_cancelled")
	case <-timer.C:
		return nil
	}
}

func flowCaptchaFailure(code string) error {
	if code == "" {
		code = "unknown"
	}
	return &publicError{Code: "flow_captcha_failed", Message: "flow_captcha_failed: " + code, HTTPStatus: 503}
}

// flowSolverCall posts one solver API call. The key travels in the body, so the
// response is never echoed into an error.
func (service *service) flowSolverCall(ctx context.Context, endpoint string, body any, target any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return failure(500, "flow_captcha_request_invalid")
	}
	callCtx, cancel := context.WithTimeout(ctx, flowCaptchaCall)
	defer cancel()
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return failure(500, "flow_captcha_request_invalid")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := service.client.Do(request)
	if err != nil {
		return failure(503, "flow_captcha_unreachable")
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || json.Unmarshal(raw, target) != nil {
		return failure(503, "flow_captcha_response_invalid")
	}
	return nil
}
