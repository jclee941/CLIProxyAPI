package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
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
	// VideoCapped is the product's own answer to whether the account may make a
	// video now, absent when it could not be read. VideoAvailableAt is when it
	// says videos come back, when it says so.
	VideoCapped      *bool    `json:"video_capped,omitempty"`
	VideoAvailableAt *float64 `json:"video_available_at,omitempty"`
}
type accountView struct {
	ID             string             `json:"id"`
	Label          string             `json:"label"`
	Enabled        bool               `json:"enabled"`
	Status         string             `json:"status"`
	Models         []accountModelView `json:"models"`
	Usage          *usageView         `json:"usage"`
	Error          string             `json:"error,omitempty"`
	ObservedAt     float64            `json:"observed_at"`
	AutoResolvedAt float64            `json:"auto_resolved_at,omitempty"`
	Activity       *accountActivity   `json:"activity,omitempty"`
}

type accountActivity struct {
	Model     string `json:"model"`
	StartedAt int64  `json:"started_at,omitempty"`
	Summary   string `json:"summary,omitempty"`
}

type accountListResponse struct {
	Accounts []accountView `json:"accounts"`
	Provider string        `json:"provider"`
	// GenerationUnits is what the scheduler takes one video to cost; a new room
	// needs three of them left in both windows.
	GenerationUnits float64 `json:"generation_units"`
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
	record.Disabled = record.Disabled || runtime.Auth.Disabled || entry.Disabled
	return record, !record.Disabled, nil
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
	view.AutoResolvedAt = float64(service.autoReleaseIfInterrupted(ctx, record))
	view.Activity = service.runningTurn(record)
	// A running turn locks the session against new work, but the credential
	// behind it still answers, so models and usage are read the same way.
	token, err := service.inspectLocalAccount(ctx, record)
	if view.Activity != nil && err != nil {
		token, err = service.busySessionToken(record)
	}
	if err != nil {
		return failedAccount(view, err)
	}
	account, err := service.accountModels(ctx, record.TokenRef, token)
	if err != nil {
		return failedAccount(view, err)
	}
	view.Status = "ready"
	if view.Activity != nil {
		view.Status = "generating"
	}
	if account.ObservedAt > 0 {
		view.ObservedAt = account.ObservedAt
	}
	for _, model := range service.interactionModels(account) {
		view.Models = append(view.Models, accountModelView{model.ID, model.DisplayName})
	}
	view.Usage, err = service.usage(ctx, record.TokenRef, token)
	if err != nil {
		// A usage figure that could not be read says nothing about whether the
		// account can serve a turn, and failing the whole card over it took six
		// working accounts out of rotation at once. The dashboard already has a
		// state for an absent reading, and the scheduler treats an unmeasured
		// account as full rather than as spent.
		return view
	}
	service.observeQuota(record.ID, view.Usage)
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

// webUsageRPC reports the account's quota windows. It is the same batchexecute
// envelope the capability RPC uses, so reading usage needs no transport of its
// own.
const webUsageRPC = "jSf9Qc"

// usageWindows maps the metric type the RPC reports onto the window a reader
// recognises. The order these are rendered in is decided later; this is only
// what each number means.
var usageWindows = map[int]string{1: "5h", 2: "weekly", 3: "ai_credit"}

// webVideoBudgetRPC is CheckGxuBudget, what the web app asks before it lets the
// video tool send. The windows do not answer that: an account read 96% of its
// week left and was still out of videos, and the product then took every turn
// and answered it as a text model instead of saying so.
const webGxuBudgetRPC = "MyzX6c"

// webVideoCap reads the video cap out of a CheckGxuBudget body the way the web
// app does: an entry with action 5 in state 3 is its "You're out of videos for
// now" notice, and the entry's timestamp is when videos come back. A body with
// no such entry is an account that may make videos. ok is false when the body
// is not a budget at all.
func webVideoCap(body any) (capped bool, until *float64, ok bool) {
	if _, message := body.([]any); !message {
		return false, nil, false
	}
	entries, _ := webJSPBField(body, 1).([]any)
	for _, entry := range entries {
		action, _ := jsonInteger(webJSPBField(entry, 0))
		state, _ := jsonInteger(webJSPBField(entry, 5))
		if action == 5 && state == 3 {
			return true, jsonNumber(jsonField(webJSPBField(entry, 4), 0)), true
		}
	}
	return false, nil, true
}

// videoCap asks the product whether the account may make a video now. A budget
// that could not be read is unknown rather than open, so the scheduler keeps
// what it last knew.
func (session *webSession) videoCap(ctx context.Context) (*bool, *float64) {
	body, err := session.rpc(ctx, webGxuBudgetRPC, []any{})
	if err != nil {
		return nil, nil
	}
	capped, until, ok := webVideoCap(body)
	if !ok {
		return nil, nil
	}
	return &capped, until
}

// nativeUsage reads the quota windows from the web product. The sidecar answered
// this once and was the last thing the plugin could not do for itself.
func (service *service) nativeUsage(ctx context.Context, reference string, token sessionToken) (*usageView, error) {
	credential, err := decodeWebCredential(token)
	if err != nil {
		return nil, err
	}
	session := service.newSession(credential)
	service.trackJar(reference, session)
	defer service.persistJar(reference, session)
	return service.webUsage(ctx, session)
}

// webUsage reads the windows and the video budget on a session already open, so
// a turn can read what it spent on the session that spent it.
func (service *service) webUsage(ctx context.Context, session *webSession) (*usageView, error) {
	body, err := session.rpc(ctx, webUsageRPC, []any{})
	if err != nil {
		return nil, err
	}
	result, err := service.webWindows(body)
	if err != nil {
		return nil, err
	}
	result.VideoCapped, result.VideoAvailableAt = session.videoCap(ctx)
	return result, nil
}

// webWindows decodes the quota windows jSf9Qc reports.
func (service *service) webWindows(body any) (*usageView, error) {
	rows, ok := body.([]any)
	if !ok || len(rows) == 0 {
		return nil, failure(502, "usage_response_invalid")
	}
	result := &usageView{Source: "GoogleWeb", ObservedAt: float64(service.now().Unix())}
	if code, present := jsonInteger(jsonField(body, 0)); present {
		tier := code
		result.TierCode = &tier
		if code == 2 {
			pro := "PRO"
			result.Tier = &pro
		}
	}
	measured, present := jsonField(body, 1).([]any)
	if !present {
		return result, nil
	}
	result.Metrics = make([]usageMetric, 0, len(measured))
	for _, row := range measured {
		metric := usageMetric{Unit: "provider_compute_unit", WindowKind: "unknown"}
		if kind, ok := jsonInteger(jsonField(row, 2)); ok {
			if window, named := usageWindows[kind]; named {
				metric.WindowKind = window
			}
		}
		metric.RemainingUnits = jsonNumber(jsonField(row, 0))
		metric.UsageFraction = jsonNumber(jsonField(row, 1))
		metric.ResetUnixSeconds = jsonNumber(jsonField(row, 3, 0, 0))
		// The fraction is what the account reports; the percentage is what every
		// reader of this actually displays, and it was the upstream that used to
		// derive it. It is rounded because it is only ever shown: multiplying the
		// fraction by 100 put readings like 53.578610000000005% on screen.
		if metric.UsageFraction != nil {
			percent := math.Round(*metric.UsageFraction*100*100) / 100
			metric.UsagePercent = &percent
		}
		result.Metrics = append(result.Metrics, metric)
	}
	orderUsageWindows(result.Metrics)
	return result, nil
}

func (service *service) usage(ctx context.Context, reference string, token sessionToken) (*usageView, error) {
	if service.settings().NativeGeneration {
		return service.nativeUsage(ctx, reference, token)
	}
	response, err := service.sidecar(ctx, sidecarRequest{Method: "GET", Path: "/v1/usage", Token: token, Reference: reference})
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
	orderUsageWindows(result.Metrics)
	return result, nil
}

// orderUsageWindows puts the windows in one order for every account. Google
// returns them in per-account order, so the dashboard showed 5h and weekly
// swapped between cards and between refreshes. Both readers of the product
// have to do this: the native one was added without it and brought the swap
// back.
func orderUsageWindows(metrics []usageMetric) {
	sort.SliceStable(metrics, func(first, second int) bool {
		return usageWindowRank(metrics[first].WindowKind) < usageWindowRank(metrics[second].WindowKind)
	})
}

func usageWindowRank(window string) int {
	switch window {
	case "5h":
		return 0
	case "weekly":
		return 1
	case "ai_credit":
		return 2
	default:
		return 3
	}
}
