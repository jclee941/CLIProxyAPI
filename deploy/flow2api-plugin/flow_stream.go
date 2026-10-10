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

type flowStreamKind uint8

const (
	flowCreationStream flowStreamKind = iota
	flowAppletStream
)

const (
	flowCreationStreamPath = "/_/AiSandboxAngularFrontend/data/google.internal.labs.aisandbox.proto.flow.agent.v1.FlowCreationAgentService/StreamChat"
	flowAppletStreamPath   = "/_/AiSandboxAngularFrontend/data/google.internal.labs.aisandbox.proto.flow.agent.v1.FlowAppletAgentService/RunAppletAgentSse"
)

var flowRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func flowRequestID(value string) (string, error) {
	if value == "" {
		return flowID(), nil
	}
	if !flowRequestIDPattern.MatchString(value) {
		return "", failure(400, "flow_request_id_invalid")
	}
	return value, nil
}

// Stream mutations are submitted once. The broker owns the upstream connection
// and its cancellation handle; a lost response must be recovered from history.
func (service *service) flowStream(ctx context.Context, record storageRecord, project, requestID string, kind flowStreamKind, build func(string) any) ([]any, error) {
	path, action := flowCreationStreamPath, "CHAT_GENERATION"
	switch kind {
	case flowCreationStream:
	case flowAppletStream:
		path, action = flowAppletStreamPath, "APP_GENERATION"
	default:
		return nil, failure(400, "flow_stream_kind_invalid")
	}
	token, err := service.flowToken(ctx, record, action, flowOrigin+"/project/"+project)
	if err != nil {
		return nil, err
	}
	var events []any
	err = service.withFlowSession(ctx, record, func(session *flowSession) error {
		if session.page.build == "" || time.Since(session.page.read) > flowPageAge {
			if err := session.bootstrap(ctx, "/project/"+project); err != nil {
				return err
			}
		}
		inner, err := json.Marshal(build(token.value))
		if err != nil {
			return failure(400, "flow_request_invalid")
		}
		outer, err := json.Marshal([]any{nil, string(inner)})
		if err != nil {
			return failure(400, "flow_request_invalid")
		}
		query := url.Values{
			"bl": {session.page.build}, "f.sid": {session.page.sessionID}, "hl": {"en"},
			"_reqid": {strconv.Itoa(session.requestID)}, "rt": {"c"},
		}
		form := url.Values{"f.req": {string(outer)}, "at": {session.page.xsrf}}
		headers := http.Header{
			"Content-Type": {"application/x-www-form-urlencoded;charset=UTF-8"},
			"Origin":       {flowOrigin}, "Referer": {flowOrigin + "/project/" + project}, "X-Same-Domain": {"1"},
			"User-Agent": {token.userAgent},
		}
		if token.userAgent == "" {
			headers.Set("User-Agent", flowUserAgent)
		}
		reply, err := service.exchangeWithRequestID(ctx, record.SourceAuthID, "POST", session.origin+path+"?"+query.Encode(), []byte(form.Encode()), headers, requestID)
		if err != nil {
			return err
		}
		if reply.StatusCode == 401 {
			return failure(401, "flow_unauthenticated")
		}
		if reply.StatusCode < 200 || reply.StatusCode >= 300 {
			return &publicError{Code: "flow_upstream_status", Message: fmt.Sprintf("flow_upstream_status: HTTP %d", reply.StatusCode), HTTPStatus: 502}
		}
		if match := flowXSRFPattern.FindSubmatch(reply.Body); match != nil {
			session.page.xsrf = string(match[1])
			return failure(409, "flow_page_token_refused")
		}
		events, err = decodeFlowStream(reply.Body)
		return err
	})
	switch safeCredentialCode(err) {
	case "flow_transport_failed", "flow_response_failed", "flow_response_invalid",
		"flow_session_exchange_transport_failed", "flow_session_exchange_response_failed", "flow_session_exchange_response_too_large":
		return nil, failure(502, "flow_submission_outcome_unknown")
	}
	return events, err
}

func decodeFlowStream(raw []byte) ([]any, error) {
	frames, err := batchFrames(raw)
	if err != nil {
		return nil, err
	}
	events := []any{}
	for _, frame := range frames {
		switch jsonField(frame, 0) {
		case "wrb.fr":
			if code, ok := jsonInteger(jsonField(frame, 5, 0)); ok && code != 0 {
				return nil, &publicError{Code: "flow_rpc_error", Message: fmt.Sprintf("flow_rpc_error: RPC_CODE_%d", code), HTTPStatus: 502}
			}
			encoded, ok := jsonField(frame, 2).(string)
			if !ok {
				return nil, failure(502, "flow_stream_response_invalid")
			}
			var event []any
			if json.Unmarshal([]byte(encoded), &event) != nil {
				return nil, failure(502, "flow_stream_response_invalid")
			}
			events = append(events, event)
		case "er":
			return nil, failure(502, "flow_stream_rejected")
		}
	}
	if len(events) == 0 {
		return nil, failure(502, "flow_stream_empty")
	}
	return events, nil
}

func (service *service) cancelFlowRequest(ctx context.Context, record storageRecord, id string, request flowHTTPRequest) (httpResponse, error) {
	id, action, found := strings.Cut(id, ":")
	if !found || action != "cancel" || request.Method != http.MethodPost || !flowRequestIDPattern.MatchString(id) {
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	if len(request.Body) != 0 {
		var empty struct{}
		if err := flowStrict("request_cancel", request.Body, &empty); err != nil {
			return httpResponse{}, err
		}
	}
	var result struct {
		Requested bool  `json:"requested"`
		Active    *bool `json:"active"`
	}
	err := service.flowBroker(ctx, "/session/exchange/cancel", struct {
		AuthID    string `json:"auth_id"`
		RequestID string `json:"request_id"`
	}{record.SourceAuthID, id}, &result)
	if err != nil {
		return httpResponse{}, err
	}
	if !result.Requested || result.Active == nil {
		return httpResponse{}, failure(502, "flow_cancel_response_invalid")
	}
	return flowJSON(200, map[string]any{"requestId": id, "requested": result.Requested, "active": *result.Active})
}
