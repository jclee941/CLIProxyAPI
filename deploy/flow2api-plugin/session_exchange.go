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
	return service.exchangeWithRequestID(ctx, accountID, method, target, body, headers, "")
}

func (service *service) exchangeWithRequestID(ctx context.Context, accountID, method, target string, body []byte, headers http.Header, requestID string) (exchangeResult, error) {
	var result exchangeResult
	err := service.flowBroker(ctx, "/session/exchange", struct {
		AuthID    string      `json:"auth_id"`
		Method    string      `json:"method"`
		URL       string      `json:"url"`
		Headers   http.Header `json:"headers"`
		Body      []byte      `json:"body"`
		RequestID string      `json:"request_id,omitempty"`
	}{accountID, method, target, headers, body, requestID}, &result)
	if err != nil {
		return exchangeResult{}, err
	}
	if result.StatusCode < 100 {
		return exchangeResult{}, failure(502, "flow_response_invalid")
	}
	return result, nil
}

func (service *service) flowBroker(ctx context.Context, path string, input, result any) error {
	key := brokerCredential()
	if key == "" {
		return failure(503, "flow_session_broker_unconfigured")
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return failure(400, "flow_request_invalid")
	}
	endpoint := strings.TrimRight(service.settings().SessionBrokerURL, "/") + "/v0/management/plugins/gemini-web" + path
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return failure(503, "flow_session_broker_unconfigured")
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	response, err := service.client.Do(request)
	if err != nil {
		return failure(502, "flow_transport_failed")
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 48*1024*1024+1))
	if err != nil || len(raw) > 48*1024*1024 {
		return failure(502, "flow_response_failed")
	}
	if response.StatusCode != http.StatusOK {
		var detail struct {
			Error string `json:"error"`
		}
		code := "flow_session_exchange_failed"
		if json.Unmarshal(raw, &detail) == nil && strings.HasPrefix(detail.Error, "session_exchange_") {
			code = "flow_" + detail.Error
		}
		return failure(response.StatusCode, code)
	}
	if json.Unmarshal(raw, result) != nil {
		return failure(502, "flow_response_invalid")
	}
	return nil
}
