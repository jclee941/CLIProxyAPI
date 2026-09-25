package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

const accountBodyLimit = 4 * 1024 * 1024
const vendorDisabled = "\u7981\u7528"

type webAccountCapabilities struct {
	PreserveDisabledAccounts bool `json:"preserve_disabled_accounts"`
}

type safeWebAccountView struct {
	ID                     webAccountID `json:"id"`
	Label                  string       `json:"label"`
	Disabled               bool         `json:"disabled"`
	Status                 string       `json:"status"`
	SourceType             *string      `json:"source_type"`
	Type                   *string      `json:"type,omitempty"`
	TrackedImageRemaining  *float64     `json:"tracked_image_remaining"`
	ObservedImageRemaining *float64     `json:"observed_image_remaining"`
	ResetAfterSeconds      *float64     `json:"reset_after_seconds"`
	ObservedAt             *string      `json:"observed_at"`
	ObservationSource      string       `json:"observation_source"`
	RefreshError           *string      `json:"refresh_error"`
}

type vendorAccount struct {
	AccessToken           string          `json:"access_token"`
	Status                string          `json:"status"`
	SourceType            string          `json:"source_type"`
	Type                  string          `json:"type"`
	Quota                 json.RawMessage `json:"quota"`
	LimitsProgress        json.RawMessage `json:"limits_progress"`
	LastRefreshError      json.RawMessage `json:"last_refresh_error"`
	LastTokenRefreshError json.RawMessage `json:"last_token_refresh_error"`
}

type vendorSnapshot struct {
	Items        []vendorAccount `json:"items"`
	Capabilities json.RawMessage `json:"capabilities"`
}

func (snapshot vendorSnapshot) preservesDisabled() bool {
	var capabilities struct {
		PreserveDisabled json.RawMessage `json:"preserve_disabled_accounts"`
	}
	return json.Unmarshal(snapshot.Capabilities, &capabilities) == nil && string(capabilities.PreserveDisabled) == "1"
}

type privateWebAPI struct {
	baseURL, key string
	client       *http.Client
}

func (plugin *service) webAPI() (privateWebAPI, *publicError) {
	key := os.Getenv("CHATGPT2API_AUTH_KEY")
	if !boundedText(key, 32768) {
		return privateWebAPI{}, failure(503, "web_api_unconfigured_set_CHATGPT2API_AUTH_KEY")
	}
	return privateWebAPI{strings.TrimSuffix(plugin.settings().APIBaseURL, "/"), key, plugin.accountClient}, nil
}

func (api privateWebAPI) request(ctx context.Context, method, path string, body []byte) (raw []byte, resultError *publicError) {
	request, err := http.NewRequestWithContext(ctx, method, api.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, failure(502, "web_api_unavailable")
	}
	request.Header.Set("Authorization", "Bearer "+api.key)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, errDo := api.client.Do(request)
	if errDo != nil {
		return nil, failure(502, "web_api_unavailable")
	}
	defer func() {
		if errClose := response.Body.Close(); errClose != nil && resultError == nil {
			raw = nil
			resultError = failure(502, "web_api_unavailable")
		}
	}()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, failure(502, "web_api_redirect_blocked")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, failure(502, "web_api_http_error")
	}
	raw, errRead := io.ReadAll(io.LimitReader(response.Body, accountBodyLimit+1))
	if errRead != nil {
		return nil, failure(502, "web_api_unavailable")
	}
	if len(raw) > accountBodyLimit {
		return nil, failure(502, "web_api_response_too_large")
	}
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, failure(502, "web_api_invalid_response")
	}
	return raw, nil
}

func parseSnapshot(raw []byte) (vendorSnapshot, *publicError) {
	var snapshot vendorSnapshot
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.Items == nil {
		return vendorSnapshot{}, failure(502, "web_api_invalid_response")
	}
	seen := make(map[string]bool, len(snapshot.Items))
	for _, account := range snapshot.Items {
		if _, err := parseAccess(account.AccessToken); err != nil || seen[account.AccessToken] {
			return vendorSnapshot{}, failure(502, "web_api_invalid_response")
		}
		seen[account.AccessToken] = true
	}
	return snapshot, nil
}

func (api privateWebAPI) accounts(ctx context.Context) (vendorSnapshot, *publicError) {
	raw, err := api.request(ctx, http.MethodGet, "/api/accounts", nil)
	if err != nil {
		return vendorSnapshot{}, err
	}
	return parseSnapshot(raw)
}

func (account vendorAccount) id() webAccountID {
	return webAccountID(opaqueID("web", account.AccessToken))
}

func metric(raw json.RawMessage) *float64 {
	var value float64
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &value) != nil || value < 0 || value > 9007199254740991 || math.Trunc(value) != value {
		return nil
	}
	return &value
}

func enumValue(value string, allowed ...string) *string {
	for _, candidate := range allowed {
		if candidate == value {
			return &value
		}
	}
	return nil
}

func (account vendorAccount) view(now time.Time) safeWebAccountView {
	id := account.id()
	view := safeWebAccountView{ID: id, Label: "Web " + string(id)[4:16], Status: "unknown", Disabled: account.Status == vendorDisabled, TrackedImageRemaining: metric(account.Quota), ObservationSource: "stored_snapshot"}
	switch account.Status {
	case vendorDisabled:
		view.Status = "disabled"
	case "\u6b63\u5e38":
		view.Status = "normal"
	case "\u9650\u6d41":
		view.Status = "limited"
	case "\u5f02\u5e38":
		view.Status = "abnormal"
	}
	view.SourceType = enumValue(account.SourceType, "web", "codex", "oauth_login", "cpa", "sub2api")
	view.Type = enumValue(account.Type, "free", "Free", "plus", "Plus", "pro", "Pro", "prolite", "ProLite", "team", "Team", "business", "Business", "enterprise", "Enterprise")
	var limits []struct {
		Feature    string          `json:"feature_name"`
		Remaining  json.RawMessage `json:"remaining"`
		ResetAfter json.RawMessage `json:"reset_after"`
	}
	if json.Unmarshal(account.LimitsProgress, &limits) == nil {
		found := false
		for _, limit := range limits {
			if limit.Feature != "image_gen" {
				continue
			}
			if found {
				view.ObservedImageRemaining = nil
				view.ResetAfterSeconds = nil
				break
			}
			found = true
			view.ObservedImageRemaining = metric(limit.Remaining)
			var resetText string
			if json.Unmarshal(limit.ResetAfter, &resetText) == nil {
				if resetAt, err := time.Parse(time.RFC3339Nano, resetText); err == nil {
					seconds := math.Max(0, resetAt.Sub(now).Seconds())
					view.ResetAfterSeconds = &seconds
				}
			}
		}
	}
	for _, raw := range []json.RawMessage{account.LastRefreshError, account.LastTokenRefreshError} {
		if len(raw) != 0 && string(raw) != "null" && string(raw) != `""` {
			code := "vendor_refresh_failed"
			view.RefreshError = &code
		}
	}
	return view
}
