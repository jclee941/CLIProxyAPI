package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
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
	switch {
	case request.Method == "GET" && request.Path == accountsPath:
		entries, err := service.entries(request.HostCallbackID)
		if err != nil {
			return nil, err
		}
		accounts := make([]accountView, 0, len(entries))
		for _, entry := range entries {
			record, enabled, err := service.getRecord(request.HostCallbackID, entry)
			if err != nil {
				accounts = append(accounts, failedAccount(accountView{ID: entry.ID, Label: "Unavailable account", Enabled: !entry.Disabled, Models: []accountModelView{}, ObservedAt: float64(service.now().UnixMilli()) / 1000}, err))
				continue
			}
			accounts = append(accounts, service.inspectAccount(ctx, record, enabled))
		}
		return struct {
			Accounts []accountView `json:"accounts"`
			Provider string        `json:"provider"`
		}{accounts, provider}, nil
	case request.Method == "POST" && request.Path == accountsPath:
		return service.registerAccount(ctx, request)
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
		return service.inspectAccount(ctx, record, enabled), nil
	default:
		return nil, failure(404, "management_route_not_found")
	}
}

func (service *service) registerAccount(ctx context.Context, request managementRequest) (interface{}, error) {
	var body struct {
		Label      string `json:"label"`
		Token      string `json:"token"`
		TokenRef   string `json:"token_ref"`
		ExistingID string `json:"existing_id"`
	}
	if len(request.Body) > 40000 || strictJSON(request.Body, &body) != nil || strings.TrimSpace(body.Label) == "" || len(body.Label) > 200 || strings.ContainsAny(body.Label, "\r\n\x00") || (body.Token == "") == (body.TokenRef == "") {
		return nil, failure(400, "invalid_account_registration")
	}
	record := storageRecord{Type: provider, Label: body.Label}
	existing := secretReference{}
	if body.ExistingID != "" {
		previous, enabled, err := service.findRecord(request.HostCallbackID, body.ExistingID)
		if err != nil {
			return nil, err
		}
		if !enabled {
			return nil, failure(409, "disabled_account_update_requires_host_enable")
		}
		record = previous
		record.Label = body.Label
		existing, err = parseReference(previous.TokenRef, service.settings().Vault)
		if err != nil {
			return nil, err
		}
	} else {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, failure(500, "account_id_failed")
		}
		record.ID = "gemini-web-" + hex.EncodeToString(random[:]) + ".json"
	}
	var token sessionToken
	var reference secretReference
	var err error
	if body.TokenRef != "" {
		reference, err = parseReference(body.TokenRef, service.settings().Vault)
		if err != nil {
			return nil, err
		}
		token, err = service.secrets.Resolve(ctx, reference)
	} else {
		token, err = parseToken(body.Token)
	}
	if err != nil {
		return nil, err
	}
	if _, err := service.accountModels(ctx, token); err != nil {
		return nil, err
	}
	if body.Token != "" {
		reference, err = service.secrets.Put(ctx, secretWrite{Label: body.Label, Token: token, Existing: existing})
		if err != nil {
			return nil, err
		}
	}
	record.TokenRef = reference.value
	auth, err := authFromRecord(record)
	if err != nil {
		return nil, err
	}
	var saved struct {
		Name string `json:"name"`
	}
	if err := service.callback("host.auth.save", callbackRequest{HostCallbackID: request.HostCallbackID, Name: record.ID, JSON: auth.StorageJSON}, &saved); err != nil {
		return nil, failure(503, "auth_save_failed_reference_retained_in_1password")
	}
	return struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}{record.ID, "ready"}, nil
}
