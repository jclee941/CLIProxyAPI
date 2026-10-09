package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type exchangeResult struct {
	StatusCode int         `json:"status_code"`
	Headers    http.Header `json:"headers"`
	Body       []byte      `json:"body"`
}

func (service *service) exchange(ctx context.Context, accountID, method, target string, body []byte, headers http.Header) (exchangeResult, error) {
	key := brokerCredential()
	if key == "" {
		return exchangeResult{}, failure(503, "flow_session_broker_unconfigured")
	}
	encoded, err := json.Marshal(struct {
		AuthID  string      `json:"auth_id"`
		Method  string      `json:"method"`
		URL     string      `json:"url"`
		Headers http.Header `json:"headers"`
		Body    []byte      `json:"body"`
	}{accountID, method, target, headers, body})
	if err != nil {
		return exchangeResult{}, failure(400, "flow_request_invalid")
	}
	endpoint := strings.TrimRight(service.settings().SessionBrokerURL, "/") + "/v0/management/plugins/gemini-web/session/exchange"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return exchangeResult{}, failure(503, "flow_session_broker_unconfigured")
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	response, err := service.client.Do(request)
	if err != nil {
		return exchangeResult{}, failure(502, "flow_transport_failed")
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 48*1024*1024+1))
	if err != nil || len(raw) > 48*1024*1024 {
		return exchangeResult{}, failure(502, "flow_response_failed")
	}
	if response.StatusCode != http.StatusOK {
		var detail struct {
			Error string `json:"error"`
		}
		code := "flow_session_exchange_failed"
		if json.Unmarshal(raw, &detail) == nil && strings.HasPrefix(detail.Error, "session_exchange_") {
			code = "flow_" + detail.Error
		}
		return exchangeResult{}, failure(response.StatusCode, code)
	}
	var result exchangeResult
	if json.Unmarshal(raw, &result) != nil || result.StatusCode < 100 {
		return exchangeResult{}, failure(502, "flow_response_invalid")
	}
	return result, nil
}
