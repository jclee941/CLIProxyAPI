package main

import (
	"context"
	"encoding/json"
	"errors"
)

type accountModelView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type usageMetric struct {
	RemainingUnits   *float64 `json:"remaining_units"`
	UsageFraction    *float64 `json:"usage_fraction"`
	UsagePercent     *float64 `json:"usage_percent,omitempty"`
	ResetUnixSeconds *float64 `json:"reset_unix_seconds"`
	WindowKind       string   `json:"window_kind"`
	Unit             string   `json:"unit"`
}
type usageView struct {
	Tier       *string       `json:"tier"`
	TierCode   *int          `json:"tier_code"`
	Metrics    []usageMetric `json:"metrics"`
	Source     string        `json:"source"`
	Estimated  bool          `json:"estimated"`
	ObservedAt float64       `json:"observed_at"`
}
type accountView struct {
	ID         string             `json:"id"`
	Label      string             `json:"label"`
	Enabled    bool               `json:"enabled"`
	Status     string             `json:"status"`
	Models     []accountModelView `json:"models"`
	Usage      *usageView         `json:"usage"`
	Error      string             `json:"error,omitempty"`
	ObservedAt float64            `json:"observed_at"`
}

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
	var envelope envelope
	if json.Unmarshal(response, &envelope) != nil || !envelope.OK {
		return failure(503, "host_auth_store_unavailable")
	}
	if json.Unmarshal(envelope.Result, output) != nil {
		return failure(502, "host_auth_response_invalid")
	}
	return nil
}

func (service *service) entries(callbackID string) ([]hostEntry, error) {
	var response struct {
		Files []hostEntry `json:"files"`
	}
	if err := service.callback("host.auth.list", callbackRequest{HostCallbackID: callbackID}, &response); err != nil {
		return nil, err
	}
	entries := make([]hostEntry, 0)
	for _, entry := range response.Files {
		if entry.Provider == provider || entry.Provider == "" && entry.Type == provider {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func (service *service) getRecord(callbackID string, entry hostEntry) (storageRecord, bool, error) {
	var runtime struct {
		Auth hostEntry `json:"auth"`
	}
	request := callbackRequest{HostCallbackID: callbackID, AuthIndex: entry.AuthIndex}
	if err := service.callback("host.auth.get_runtime", request, &runtime); err != nil {
		return storageRecord{}, false, err
	}
	if runtime.Auth.Provider != provider || runtime.Auth.ID != entry.ID {
		return storageRecord{}, false, failure(400, "auth_identity_mismatch")
	}
	var response struct {
		JSON json.RawMessage `json:"json"`
	}
	if err := service.callback("host.auth.get", request, &response); err != nil {
		return storageRecord{}, false, err
	}
	record, err := service.parseStorage(response.JSON, true)
	if err != nil {
		return record, false, err
	}
	if record.ID != entry.ID || entry.Name != record.ID {
		return record, false, failure(400, "auth_identity_mismatch")
	}
	return record, !runtime.Auth.Disabled, nil
}

func (service *service) findRecord(callbackID, id string) (storageRecord, bool, error) {
	if !accountIDPattern.MatchString(id) {
		return storageRecord{}, false, failure(400, "invalid_account_id")
	}
	entries, err := service.entries(callbackID)
	if err != nil {
		return storageRecord{}, false, err
	}
	for _, entry := range entries {
		if entry.ID == id {
			return service.getRecord(callbackID, entry)
		}
	}
	return storageRecord{}, false, failure(404, "account_not_found")
}

func (service *service) inspectAccount(ctx context.Context, record storageRecord, enabled bool) accountView {
	view := accountView{ID: record.ID, Label: record.Label, Enabled: enabled, Status: "unknown", Models: []accountModelView{}, ObservedAt: float64(service.now().UnixMilli()) / 1000}
	reference, err := parseReference(record.TokenRef, service.settings().Vault)
	if err != nil {
		return failedAccount(view, err)
	}
	token, err := service.secrets.Resolve(ctx, reference)
	if err != nil {
		return failedAccount(view, err)
	}
	account, err := service.accountModels(ctx, token)
	if err != nil {
		return failedAccount(view, err)
	}
	view.Status = "ready"
	if account.ObservedAt > 0 {
		view.ObservedAt = account.ObservedAt
	}
	for _, model := range verifiedModels(account) {
		view.Models = append(view.Models, accountModelView{model.ID, model.DisplayName})
	}
	view.Usage, err = service.usage(ctx, token)
	if err != nil {
		return failedAccount(view, err)
	}
	return view
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

func (service *service) usage(ctx context.Context, token sessionToken) (*usageView, error) {
	response, err := service.sidecar(ctx, sidecarRequest{Method: "GET", Path: "/v1/usage", Token: token})
	if err != nil {
		return nil, err
	}
	var wire struct {
		Tier     *string `json:"tier"`
		TierCode *int    `json:"tier_code"`
		Metrics  []struct {
			usageMetric
			ResetAt *float64 `json:"reset_at"`
		} `json:"metrics"`
		Source     string  `json:"source"`
		Estimated  *bool   `json:"estimated"`
		ObservedAt float64 `json:"observed_at"`
	}
	if json.Unmarshal(response.Body, &wire) != nil || wire.Source != "GoogleWeb" || wire.Estimated == nil || *wire.Estimated || wire.ObservedAt <= 0 {
		return nil, failure(502, "usage_response_invalid")
	}
	if wire.Tier != nil && *wire.Tier != "PRO" {
		return nil, failure(502, "usage_tier_unknown")
	}
	result := &usageView{Tier: wire.Tier, TierCode: wire.TierCode, Source: wire.Source, ObservedAt: wire.ObservedAt}
	if wire.Metrics != nil {
		result.Metrics = make([]usageMetric, 0, len(wire.Metrics))
	}
	for _, metric := range wire.Metrics {
		if metric.Unit != "provider_compute_unit" {
			return nil, failure(502, "usage_unit_invalid")
		}
		switch metric.WindowKind {
		case "5h", "weekly", "ai_credit", "unknown":
		default:
			return nil, failure(502, "usage_window_invalid")
		}
		if metric.ResetUnixSeconds == nil {
			metric.ResetUnixSeconds = metric.ResetAt
		}
		result.Metrics = append(result.Metrics, metric.usageMetric)
	}
	return result, nil
}
