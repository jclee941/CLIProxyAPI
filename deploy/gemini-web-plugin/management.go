package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func managementJSON(status int, value interface{}) (httpResponse, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return httpResponse{}, failure(500, "response_encoding_failed")
	}
	return httpResponse{StatusCode: status, Headers: http.Header{"Content-Type": {"application/json"}, "Cache-Control": {"no-store"}}, Body: raw}, nil
}

func (service *service) management(ctx context.Context, raw []byte) (httpResponse, error) {
	var request managementRequest
	if json.Unmarshal(raw, &request) != nil {
		return managementJSON(400, struct {
			Error string `json:"error"`
		}{"invalid_request"})
	}
	if request.Method == "GET" && request.Path == resourcePath {
		body, err := os.ReadFile(service.settings().DashboardPath)
		if err != nil {
			return httpResponse{StatusCode: 503, Headers: http.Header{"Content-Type": {"text/plain"}, "Cache-Control": {"no-store"}}, Body: []byte("Dashboard asset unavailable")}, nil
		}
		return httpResponse{StatusCode: 200, Headers: http.Header{"Content-Type": {"text/html; charset=utf-8"}, "Cache-Control": {"no-store"}, "X-Content-Type-Options": {"nosniff"}, "Referrer-Policy": {"no-referrer"}}, Body: body}, nil
	}
	if request.Method == "GET" && request.Path == openapiPath {
		return openapiResource(), nil
	}
	if request.Method == "GET" && request.Path == extensionPath {
		// The companion is what a browser needs before it can hand a session over,
		// so it is served from the same place the operator already authenticates.
		body, err := os.ReadFile(filepath.Join(filepath.Dir(service.settings().DashboardPath), extensionArchive))
		if err != nil {
			return httpResponse{StatusCode: 503, Headers: http.Header{"Content-Type": {"text/plain"}, "Cache-Control": {"no-store"}}, Body: []byte("Companion extension unavailable")}, nil
		}
		return httpResponse{StatusCode: 200, Headers: http.Header{"Content-Type": {"application/zip"}, "Cache-Control": {"no-store"}, "Content-Disposition": {`attachment; filename="` + extensionArchive + `"`}, "X-Content-Type-Options": {"nosniff"}}, Body: body}, nil
	}
	result, err := service.managementOperation(ctx, request)
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

func (service *service) managementOperation(ctx context.Context, request managementRequest) (interface{}, error) {
	// A Flow session exchange borrows a credential and a refresh reads one
	// account; neither changes an account. Exchanges run many times a minute,
	// and emptying the listing cache on each one sent every dashboard read back
	// to inspecting all accounts.
	if request.Method == "POST" && request.Path != sessionExchangePath && request.Path != sessionExchangeCancelPath && request.Path != refreshPath {
		defer service.dropAccountsCache()
	}
	switch {
	case request.Method == "POST" && strings.HasPrefix(request.Path, loginPath):
		return service.loginOperation(ctx, request)
	case request.Method == "GET" && request.Path == accountsPath:
		entries, err := service.entries(request.HostCallbackID)
		if err != nil {
			return nil, err
		}
		scope := accountsScope(entries)
		cached, fresh, found := service.cachedAccounts(scope)
		if found && fresh {
			return accountListResponse{cached, provider, service.generationUnits()}, nil
		}
		epoch := service.accountsGeneration()
		refresh := service.refreshAccounts(scope, epoch, service.listedAccounts(request.HostCallbackID, entries))
		if found {
			return accountListResponse{cached, provider, service.generationUnits()}, nil
		}
		select {
		case <-refresh.done:
		case <-ctx.Done():
			return nil, failure(499, "accounts_listing_cancelled")
		}
		if refresh.views == nil {
			return nil, failure(503, "plugin_shutdown")
		}
		return accountListResponse{refresh.views, provider, service.generationUnits()}, nil
	case request.Method == "POST" && request.Path == sessionExchangePath:
		return service.sessionExchange(ctx, request)
	case request.Method == "POST" && request.Path == sessionExchangeCancelPath:
		return service.cancelSessionExchange(request)
	case request.Method == "POST" && request.Path == "/v0/management"+maintainPath:
		return service.maintain(ctx, request)
	case request.Method == "POST" && request.Path == resolvePath:
		return service.resolveIntent(ctx, request)
	case request.Method == "POST" && request.Path == recoverPath:
		return service.recoverIntent(ctx, request)
	case request.Method == "POST" && request.Path == labelPath:
		return service.relabelAccount(ctx, request)
	case request.Method == "POST" && request.Path == detachPath:
		return service.detachLegacyBinding(ctx, request)
	case request.Method == "POST" && request.Path == refreshPath:
		var body struct {
			ID string `json:"id"`
		}
		if strictJSON(request.Body, &body) != nil {
			return nil, failure(400, "invalid_refresh_request")
		}
		record, enabled, err := service.findRecord(request.HostCallbackID, body.ID)
		if err != nil {
			return nil, err
		}
		view := service.inspectAccount(ctx, record, enabled)
		service.updateCachedAccount(view)
		return view, nil
	default:
		return nil, failure(404, "management_route_not_found")
	}
}
