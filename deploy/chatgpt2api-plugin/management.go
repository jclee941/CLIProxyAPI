package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
)

type statusView struct {
	Plugin struct {
		ID         string `json:"id"`
		Version    string `json:"version"`
		RouteCount uint64 `json:"route_count"`
	} `json:"plugin"`
	Routing struct {
		Provider         string   `json:"provider"`
		Mode             string   `json:"mode"`
		ModelNames       []string `json:"model_names"`
		CredentialSource string   `json:"credential_source"`
	} `json:"routing"`
	Upstream upstreamView `json:"upstream"`
}

func (plugin *service) management(ctx context.Context, raw []byte) (json.RawMessage, *publicError) {
	var request managementRequest
	if json.Unmarshal(raw, &request) != nil || request.Method == "" || request.Path == "" {
		return httpFailure(400, "invalid_management_request")
	}
	if request.Path != statusPath && request.Path != resourcePath {
		return httpFailure(404, "not_found")
	}
	if request.Method != http.MethodGet {
		return httpFailure(405, "method_not_allowed")
	}
	if request.Path == resourcePath {
		body, err := os.ReadFile(plugin.settings().DashboardPath)
		if err != nil {
			return httpFailure(503, "dashboard_unavailable")
		}
		return encode(httpResponse{StatusCode: 200, Headers: http.Header{"Content-Type": {"text/html; charset=utf-8"}, "X-Content-Type-Options": {"nosniff"}}, Body: body})
	}
	var status statusView
	status.Plugin.ID = provider
	status.Plugin.Version = version
	status.Plugin.RouteCount = plugin.routeCount.Load()
	status.Routing.Provider = provider
	status.Routing.Mode = "native-provider"
	status.Routing.ModelNames = plugin.settings().ModelNames
	status.Routing.CredentialSource = "host-auth-manager"
	status.Upstream = plugin.health(ctx)
	body, err := encode(status)
	if err != nil {
		return nil, err
	}
	return encode(httpResponse{StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}, "Cache-Control": {"no-store"}}, Body: body})
}

func httpFailure(status int, code string) (json.RawMessage, *publicError) {
	body, err := encode(struct {
		Error string `json:"error"`
	}{code})
	if err != nil {
		return nil, err
	}
	headers := http.Header{"Content-Type": {"application/json"}, "Cache-Control": {"no-store"}}
	if status == http.StatusMethodNotAllowed {
		headers.Set("Allow", "GET")
	}
	return encode(httpResponse{StatusCode: status, Headers: headers, Body: body})
}
