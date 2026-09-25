package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
)

const accountsPath = "/v0/management/plugins/chatgpt-web/accounts"

type httpResponse struct {
	StatusCode int         `json:"status_code"`
	Headers    http.Header `json:"headers,omitempty"`
	Body       []byte      `json:"body,omitempty"`
}

type accountView struct {
	ID      string             `json:"id"`
	Label   string             `json:"label"`
	Enabled bool               `json:"enabled"`
	Status  string             `json:"status"`
	Plan    string             `json:"plan,omitempty"`
	Groups  []quotaGroup       `json:"groups"`
	Error   string             `json:"error,omitempty"`
	Sub     *quotaSubscription `json:"subscription,omitempty"`
}

func managementJSON(status int, value interface{}) (httpResponse, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return httpResponse{}, failure(500, "response_encoding_failed")
	}
	return httpResponse{StatusCode: status, Headers: http.Header{"Content-Type": {"application/json"}, "Cache-Control": {"no-store"}}, Body: raw}, nil
}

func (service *service) management(ctx context.Context, raw []byte) (interface{}, error) {
	var request managementRequest
	if json.Unmarshal(raw, &request) != nil {
		return managementJSON(400, struct {
			Error string `json:"error"`
		}{"invalid_request"})
	}
	if request.Method == "GET" && request.Path == resourcePath {
		body, readErr := os.ReadFile(service.dashboard)
		if readErr != nil {
			return httpResponse{StatusCode: 503, Headers: http.Header{"Content-Type": {"text/plain"}, "Cache-Control": {"no-store"}}, Body: []byte("Dashboard asset unavailable")}, nil
		}
		return httpResponse{StatusCode: 200, Headers: http.Header{
			"Content-Type":           {"text/html; charset=utf-8"},
			"Cache-Control":          {"no-store"},
			"X-Content-Type-Options": {"nosniff"},
			"Referrer-Policy":        {"no-referrer"},
		}, Body: body}, nil
	}
	if request.Method != "GET" || request.Path != accountsPath {
		return managementJSON(404, struct {
			Error string `json:"error"`
		}{"management_route_not_found"})
	}
	result, err := service.accounts(ctx, request.HostCallbackID)
	if err != nil {
		public := failure(500, "management_operation_failed")
		var typed *publicError
		if errors.As(err, &typed) {
			public = typed
		}
		return managementJSON(public.HTTPStatus, struct {
			Error string `json:"error"`
		}{public.Code})
	}
	return managementJSON(200, result)
}

func (service *service) accounts(ctx context.Context, callbackID string) (interface{}, error) {
	if callbackID == "" {
		return nil, failure(401, "authenticated_management_callback_required")
	}
	entries, err := service.entries(callbackID)
	if err != nil {
		return nil, err
	}
	views := make([]accountView, 0, len(entries))
	for _, entry := range entries {
		if entry.Provider != authProvider {
			continue
		}
		view := accountView{ID: entry.ID, Label: entry.Name, Enabled: !entry.Disabled, Status: "unknown", Groups: []quotaGroup{}}
		token, tokenErr := service.accessToken(callbackID, entry.ID)
		if tokenErr != nil {
			views = append(views, failedAccount(view, tokenErr))
			continue
		}
		body, usageErr := service.usage(ctx, token)
		if usageErr != nil {
			views = append(views, failedAccount(view, usageErr))
			continue
		}
		quota, mapErr := quotaFromParts(body, service.webLimits(ctx, token))
		if mapErr != nil {
			views = append(views, failedAccount(view, mapErr))
			continue
		}
		view.Status = "ready"
		view.Sub = quota.Subscription
		if quota.Subscription != nil {
			view.Plan = quota.Subscription.Plan
		}
		if quota.Groups != nil {
			view.Groups = quota.Groups
		}
		views = append(views, view)
	}
	return struct {
		Accounts []accountView `json:"accounts"`
		Provider string        `json:"provider"`
	}{views, provider}, nil
}

func failedAccount(view accountView, err error) accountView {
	view.Status = "error"
	view.Error = "account_check_failed"
	var public *publicError
	if errors.As(err, &public) {
		view.Error = public.Code
		if public.HTTPStatus == 401 || public.HTTPStatus == 403 {
			view.Status = "expired"
		}
	}
	return view
}
