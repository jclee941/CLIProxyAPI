package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type refreshProgress struct {
	Done   *bool           `json:"done"`
	Error  json.RawMessage `json:"error"`
	Result json.RawMessage `json:"result"`
}

func (api privateWebAPI) refresh(ctx context.Context, credential accessCredential, preserveDisabled bool) (vendorSnapshot, bool, *publicError) {
	body, err := json.Marshal(struct {
		AccessTokens     []string `json:"access_tokens"`
		PreserveDisabled bool     `json:"preserve_disabled,omitempty"`
	}{[]string{credential.value}, preserveDisabled})
	if err != nil {
		return vendorSnapshot{}, false, failure(500, "request_encoding_failed")
	}
	raw, errStart := api.request(ctx, http.MethodPost, "/api/accounts/refresh", body)
	if errStart != nil {
		return vendorSnapshot{}, false, errStart
	}
	var start struct {
		ProgressID string `json:"progress_id"`
	}
	if json.Unmarshal(raw, &start) != nil || len(start.ProgressID) != 36 || start.ProgressID[8] != '-' || start.ProgressID[13] != '-' || start.ProgressID[18] != '-' || start.ProgressID[23] != '-' {
		return vendorSnapshot{}, false, failure(502, "web_api_invalid_response")
	}
	if decoded, errDecode := hex.DecodeString(strings.ReplaceAll(start.ProgressID, "-", "")); errDecode != nil || len(decoded) != 16 {
		return vendorSnapshot{}, false, failure(502, "web_api_invalid_response")
	}
	for {
		rawProgress, errProgress := api.request(ctx, http.MethodGet, "/api/accounts/refresh/progress/"+start.ProgressID, nil)
		if errProgress != nil {
			return vendorSnapshot{}, false, errProgress
		}
		var progress refreshProgress
		if json.Unmarshal(rawProgress, &progress) != nil || progress.Done == nil {
			return vendorSnapshot{}, false, failure(502, "web_api_invalid_response")
		}
		if *progress.Done {
			if len(progress.Error) > 0 && string(progress.Error) != "null" && string(progress.Error) != `""` {
				return vendorSnapshot{}, false, failure(502, "vendor_refresh_failed")
			}
			snapshot, errSnapshot := parseSnapshot(progress.Result)
			if errSnapshot != nil {
				return vendorSnapshot{}, false, errSnapshot
			}
			var result struct {
				Refreshed *int              `json:"refreshed"`
				Errors    []json.RawMessage `json:"errors"`
			}
			if json.Unmarshal(progress.Result, &result) != nil || result.Refreshed == nil {
				return vendorSnapshot{}, false, failure(502, "web_api_invalid_response")
			}
			return snapshot, *result.Refreshed == 1 && len(result.Errors) == 0, nil
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return vendorSnapshot{}, false, failure(503, "request_cancelled")
		case <-timer.C:
		}
	}
}

func (plugin *service) refreshWeb(ctx context.Context, host scopedHost, body []byte) (json.RawMessage, *publicError) {
	var input struct {
		ID string `json:"id"`
	}
	if !decodeAccountInput(body, &input) || !validOpaqueID(input.ID, "web") {
		return httpFailure(400, "invalid_web_account_id")
	}
	if _, err := host.entries(ctx); err != nil {
		return httpFailure(err.HTTPStatus, err.Code)
	}
	api, errAPI := plugin.webAPI()
	if errAPI != nil {
		return httpFailure(errAPI.HTTPStatus, errAPI.Code)
	}
	snapshot, errSnapshot := api.accounts(ctx)
	if errSnapshot != nil {
		return httpFailure(errSnapshot.HTTPStatus, errSnapshot.Code)
	}
	var selected *vendorAccount
	for _, account := range snapshot.Items {
		if account.id() == webAccountID(input.ID) {
			selected = &account
			break
		}
	}
	if selected == nil {
		return httpFailure(409, "web_account_stale_reload_needed")
	}
	view := selected.view(plugin.now().UTC())
	if view.Disabled && !snapshot.preservesDisabled() {
		return httpFailure(409, "vendor_disabled_refresh_unsupported")
	}
	if view.Status == "unknown" {
		return httpFailure(409, "web_account_status_unknown")
	}
	refreshed, success, errRefresh := api.refresh(ctx, accessCredential{selected.AccessToken}, snapshot.preservesDisabled())
	if errRefresh != nil {
		return httpFailure(errRefresh.HTTPStatus, errRefresh.Code)
	}
	for _, account := range refreshed.Items {
		if account.AccessToken != selected.AccessToken {
			continue
		}
		now := plugin.now().UTC()
		observedAt := now.Format(time.RFC3339Nano)
		view = account.view(now)
		if selected.Status == vendorDisabled && !view.Disabled {
			return httpFailure(502, "vendor_disabled_contract_violated")
		}
		if success {
			view.ObservedAt = &observedAt
			view.ObservationSource = "conversation/init"
		} else {
			code := "vendor_refresh_failed"
			view.RefreshError = &code
		}
		return accountJSON(struct {
			Account    safeWebAccountView `json:"account"`
			ObservedAt string             `json:"observed_at"`
			Source     string             `json:"source"`
		}{view, observedAt, "ChatGPTWeb"})
	}
	return httpFailure(409, "web_account_stale_reload_needed")
}
