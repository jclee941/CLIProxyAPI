package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type webEnabledRequest struct {
	ID      string `json:"id"`
	Enabled *bool  `json:"enabled"`
	Consent *bool  `json:"consent"`
}

func parseWebEnabledRequest(body []byte) (webEnabledRequest, bool) {
	var input webEnabledRequest
	if !decodeAccountInput(body, &input) || !validOpaqueID(input.ID, "web") || input.Enabled == nil || input.Consent == nil || !*input.Consent {
		return webEnabledRequest{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if _, err := decoder.Token(); err != nil {
		return webEnabledRequest{}, false
	}
	seen := make(map[string]bool, 3)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return webEnabledRequest{}, false
		}
		key, valid := token.(string)
		if !valid || seen[key] || (key != "id" && key != "enabled" && key != "consent") {
			return webEnabledRequest{}, false
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return webEnabledRequest{}, false
		}
	}
	return input, true
}

func (plugin *service) setWebEnabled(ctx context.Context, host scopedHost, body []byte) (json.RawMessage, *publicError) {
	input, valid := parseWebEnabledRequest(body)
	if !valid {
		return httpFailure(400, "invalid_web_enabled_request_require_consent")
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
	if !snapshot.preservesDisabled() {
		return httpFailure(409, "vendor_web_enabled_unsupported")
	}
	for _, selected := range snapshot.Items {
		if selected.id() != webAccountID(input.ID) {
			continue
		}
		view := selected.view(plugin.now().UTC())
		if view.Status == "unknown" {
			return httpFailure(409, "web_account_status_unknown")
		}
		if *input.Enabled == view.Disabled {
			status := vendorDisabled
			if *input.Enabled {
				status = "\u6b63\u5e38"
			}
			payload, errEncode := json.Marshal(struct {
				AccessToken string `json:"access_token"`
				Status      string `json:"status"`
			}{selected.AccessToken, status})
			if errEncode != nil {
				return httpFailure(500, "request_encoding_failed")
			}
			raw, errUpdate := api.request(ctx, http.MethodPost, "/api/accounts/update", payload)
			if errUpdate != nil {
				return httpFailure(errUpdate.HTTPStatus, errUpdate.Code)
			}
			updated, errUpdated := parseSnapshot(raw)
			if errUpdated != nil {
				return httpFailure(errUpdated.HTTPStatus, errUpdated.Code)
			}
			var result struct {
				Item *vendorAccount `json:"item"`
			}
			if json.Unmarshal(raw, &result) != nil || result.Item == nil {
				return httpFailure(502, "web_api_invalid_response")
			}
			if result.Item.AccessToken != selected.AccessToken {
				return httpFailure(409, "web_account_stale_reload_needed")
			}
			var matched *vendorAccount
			for _, account := range updated.Items {
				if account.AccessToken == selected.AccessToken {
					matched = &account
					break
				}
			}
			if matched == nil {
				return httpFailure(409, "web_account_stale_reload_needed")
			}
			if result.Item.Status != status || matched.Status != status {
				return httpFailure(502, "vendor_web_enabled_contract_violated")
			}
			selected = *matched
		}
		now := plugin.now().UTC()
		return accountJSON(struct {
			Account    safeWebAccountView `json:"account"`
			Source     string             `json:"source"`
			ObservedAt string             `json:"observed_at"`
		}{selected.view(now), "ChatGPTWeb", now.Format(time.RFC3339Nano)})
	}
	return httpFailure(409, "web_account_stale_reload_needed")
}
