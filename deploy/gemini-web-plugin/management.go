package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
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
	case request.Method == "POST" && request.Path == "/v0/management"+maintainPath:
		return service.maintain(ctx, request)
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
	references := []string{}
	if existing.value != "" {
		references = append(references, existing.value)
	}
	if body.TokenRef != "" && body.TokenRef != existing.value {
		if _, err := parseReference(body.TokenRef, service.settings().Vault); err != nil {
			return nil, err
		}
		references = append(references, body.TokenRef)
	}
	sort.Strings(references)
	for _, reference := range references {
		lease, err := service.acquireCredential(reference, true)
		if err != nil {
			return nil, err
		}
		defer lease.guard.Unlock()
	}
	if body.ExistingID != "" {
		latest, enabled, err := service.findRecord(request.HostCallbackID, body.ExistingID)
		if err != nil {
			return nil, err
		}
		if !enabled {
			return nil, failure(409, "disabled_account_update_requires_host_enable")
		}
		if latest.TokenRef != existing.value {
			return nil, failure(409, "binding_mismatch")
		}
		record = latest
		record.Label = body.Label
	}
	binding, bound := service.settings().MaintenanceSources[record.ID]
	if bound && (existing.value != binding.TokenRef || body.TokenRef != "" && body.TokenRef != binding.TokenRef) {
		return nil, failure(409, "binding_mismatch")
	}
	for id, source := range service.settings().MaintenanceSources {
		if id != record.ID && (source.TokenRef == existing.value || source.TokenRef == body.TokenRef) {
			return nil, failure(409, "binding_mismatch")
		}
	}
	var expected sessionToken
	if existing.value != "" {
		expected, err = service.resolveCredential(ctx, existing, true)
		if err != nil {
			return nil, err
		}
	}
	if body.TokenRef != "" {
		reference, err = parseReference(body.TokenRef, service.settings().Vault)
		if err != nil {
			return nil, err
		}
		if reference == existing {
			token = expected
		} else {
			token, err = service.resolveCredential(ctx, reference, true)
		}
	} else {
		reference = existing
		token, err = parseToken(body.Token)
	}
	if err != nil {
		return nil, err
	}
	if bound {
		expectedUser, err := tokenAuthUser(expected)
		if err != nil {
			return nil, err
		}
		user, err := tokenAuthUser(token)
		if err != nil {
			return nil, err
		}
		if user != expectedUser || binding.AuthUser != nil && *binding.AuthUser != user {
			return nil, failure(409, "binding_mismatch")
		}
		inspection, err := service.inspectCredential(ctx, existing.value, token)
		if err != nil {
			return nil, err
		}
		if inspection.AccountSHA256 != binding.ExpectedGaiaSHA256 || inspection.AuthUser != user {
			return nil, failure(409, "binding_mismatch")
		}
	}
	if _, err := service.accountModels(ctx, reference.value, token); err != nil {
		return nil, err
	}
	if body.ExistingID != "" {
		latest, enabled, err := service.findRecord(request.HostCallbackID, body.ExistingID)
		if err != nil {
			return nil, err
		}
		if !enabled {
			return nil, failure(409, "disabled_account_update_requires_host_enable")
		}
		if latest.TokenRef != existing.value {
			return nil, failure(409, "binding_mismatch")
		}
		record = latest
		record.Label = body.Label
	}
	if body.Token != "" {
		if existing.value == "" {
			reference, err = service.putCredential(ctx, secretWrite{Label: body.Label, Token: token})
		} else {
			reference = existing
			err = service.replaceCredential(ctx, secretReplacement{Reference: existing, Expected: expected, Replacement: token})
		}
		if err != nil {
			if existing.value != "" {
				service.credentialFailure(existing.value, err)
			}
			return nil, err
		}
	}
	record.TokenRef = reference.value
	lease := service.leases.get(reference.value)
	lease.set(credentialState{state: maintenanceHostPending, tokenHash: tokenFingerprint(token)})
	if body.ExistingID != "" {
		latest, _, err := service.findRecord(request.HostCallbackID, record.ID)
		if err != nil {
			return nil, failure(503, "host_sync_pending")
		}
		if latest.TokenRef != existing.value {
			return nil, failure(409, "binding_mismatch")
		}
		record.Disabled = latest.Disabled
	}
	record.SessionRevision++
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
	lease.set(credentialState{state: maintenanceReady})
	return struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}{record.ID, "ready"}, nil
}
