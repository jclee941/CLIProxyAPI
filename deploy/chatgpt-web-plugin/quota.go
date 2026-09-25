package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func (service *service) callback(method string, request callbackRequest, output interface{}) error {
	if service.host == nil {
		return failure(503, "host_auth_store_unavailable")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return failure(500, "host_request_encoding_failed")
	}
	response, err := service.host(method, raw)
	if err != nil {
		return failure(503, "host_auth_store_unavailable")
	}
	var result envelope
	if json.Unmarshal(response, &result) != nil || !result.OK {
		return failure(503, "host_auth_store_unavailable")
	}
	if json.Unmarshal(result.Result, output) != nil {
		return failure(502, "host_auth_response_invalid")
	}
	return nil
}

func (service *service) entries(callbackID string) ([]hostEntry, error) {
	var listing struct {
		Files []hostEntry `json:"files"`
	}
	if err := service.callback("host.auth.list", callbackRequest{HostCallbackID: callbackID}, &listing); err != nil {
		return nil, err
	}
	return listing.Files, nil
}

func (service *service) accessToken(callbackID, authID string) (string, error) {
	entries, err := service.entries(callbackID)
	if err != nil {
		return "", err
	}
	index := ""
	for _, entry := range entries {
		if entry.ID == authID {
			if entry.Provider != authProvider {
				return "", failure(409, "unsupported_credential_provider")
			}
			index = entry.AuthIndex
		}
	}
	if index == "" {
		return "", failure(404, "account_not_found")
	}
	var stored struct {
		JSON json.RawMessage `json:"json"`
	}
	if err := service.callback("host.auth.get", callbackRequest{HostCallbackID: callbackID, AuthIndex: index}, &stored); err != nil {
		return "", err
	}
	var record struct {
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(stored.JSON, &record) != nil || record.AccessToken == "" {
		return "", failure(409, "credential_access_token_missing")
	}
	return record.AccessToken, nil
}

var deviceID = newDeviceID()

func newDeviceID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

// chatgpt.com rejects requests without a browser fingerprint at the edge, so these
// headers are required for a 200 even though the bearer token alone authenticates.
func applyBrowserHeaders(request *http.Request, token string) {
	header := request.Header
	header.Set("Authorization", "Bearer "+token)
	header.Set("User-Agent", browserUserAgent)
	header.Set("Accept", "*/*")
	header.Set("Accept-Language", "en-US,en;q=0.9")
	header.Set("Origin", "https://chatgpt.com")
	header.Set("Referer", "https://chatgpt.com/")
	header.Set("Sec-Ch-Ua", `"Microsoft Edge";v="143", "Chromium";v="143", "Not A(Brand";v="24"`)
	header.Set("Sec-Ch-Ua-Mobile", "?0")
	header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	header.Set("Sec-Fetch-Dest", "empty")
	header.Set("Sec-Fetch-Mode", "cors")
	header.Set("Sec-Fetch-Site", "same-origin")
	header.Set("OAI-Device-Id", deviceID)
	header.Set("OAI-Language", "en-US")
}

func (service *service) webLimits(ctx context.Context, token string) []byte {
	body, err := service.backend(ctx, http.MethodPost, conversationInitURL, token, []byte(`{"gizmo_id":null,"requested_default_model":null,"conversation_id":null,"timezone_offset_min":-480}`))
	if err != nil {
		return nil
	}
	return body
}

func (service *service) usage(ctx context.Context, token string) ([]byte, error) {
	return service.backend(ctx, http.MethodGet, usageURL, token, nil)
}

func (service *service) backend(ctx context.Context, method, url, token string, payload []byte) ([]byte, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, failure(500, "usage_request_invalid")
	}
	applyBrowserHeaders(request, token)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := service.client.Do(request)
	if err != nil {
		return nil, failure(502, "usage_transport_failed")
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if err != nil {
		return nil, failure(502, "usage_response_failed")
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, failure(response.StatusCode, "usage_credential_rejected")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, failure(502, "usage_request_failed")
	}
	return body, nil
}

func (service *service) quotaFetch(ctx context.Context, raw []byte) (interface{}, error) {
	var request struct {
		AuthID         string `json:"auth_id"`
		Provider       string `json:"provider"`
		HostCallbackID string `json:"host_callback_id"`
	}
	if json.Unmarshal(raw, &request) != nil {
		return nil, failure(400, "invalid_quota_request")
	}
	if request.Provider != "" && request.Provider != authProvider {
		return nil, failure(400, "unsupported_quota_provider")
	}
	if request.HostCallbackID == "" {
		return nil, failure(401, "authenticated_management_callback_required")
	}
	token, err := service.accessToken(request.HostCallbackID, request.AuthID)
	if err != nil {
		return nil, err
	}
	body, err := service.usage(ctx, token)
	if err != nil {
		return nil, err
	}
	return quotaFromParts(body, service.webLimits(ctx, token))
}
