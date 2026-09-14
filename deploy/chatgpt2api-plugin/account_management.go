package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const accountPrefix = "/v0/management/plugins/chatgpt2api/"

func accountRouteMethod(path string) string {
	switch path {
	case accountPrefix + "codex-sources", accountPrefix + "webaccounts":
		return http.MethodGet
	case accountPrefix + "import-codex", accountPrefix + "refresh-web", accountPrefix + "set-web-enabled":
		return http.MethodPost
	default:
		return ""
	}
}

func validOpaqueID(id, prefix string) bool {
	if len(id) != len(prefix)+65 || !strings.HasPrefix(id, prefix+"_") {
		return false
	}
	_, err := hex.DecodeString(id[len(prefix)+1:])
	return err == nil && strings.ToLower(id) == id
}

func decodeAccountInput[Value any](body []byte, output *Value) bool {
	if len(body) > 4096 || !utf8.Valid(body) || len(bytes.TrimSpace(body)) == 0 || bytes.TrimSpace(body)[0] != '{' {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	return decoder.Decode(output) == nil && decoder.Decode(new(json.RawMessage)) == io.EOF
}

func accountJSON[Value any](value Value) (json.RawMessage, *publicError) {
	body, err := encode(value)
	if err != nil {
		return nil, err
	}
	return encode(httpResponse{StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}, "Cache-Control": {"no-store"}}, Body: body})
}

func (plugin *service) accountManagement(ctx context.Context, request managementRequest, raw []byte) (json.RawMessage, *publicError) {
	if request.Method != accountRouteMethod(request.Path) {
		body := []byte(`{"error":"method_not_allowed"}`)
		return encode(httpResponse{StatusCode: 405, Headers: http.Header{"Allow": {accountRouteMethod(request.Path)}, "Content-Type": {"application/json"}, "Cache-Control": {"no-store"}}, Body: body})
	}
	var wire struct {
		CallbackID string `json:"host_callback_id"`
		Body       json.RawMessage
	}
	if !utf8.Valid(raw) || json.Unmarshal(raw, &wire) != nil {
		return httpFailure(400, "invalid_management_request")
	}
	host, err := plugin.sourceScope(wire.CallbackID)
	if err != nil {
		return httpFailure(err.HTTPStatus, err.Code)
	}
	var body []byte
	if request.Method == http.MethodPost && (len(wire.Body) > 8192 || json.Unmarshal(wire.Body, &body) != nil) {
		return httpFailure(400, "invalid_account_request")
	}
	switch request.Path {
	case accountPrefix + "codex-sources":
		sources, errSources := host.sources(ctx)
		if errSources != nil {
			return httpFailure(errSources.HTTPStatus, errSources.Code)
		}
		return accountJSON(struct {
			Sources []codexSourceView `json:"sources"`
		}{sources})
	case accountPrefix + "import-codex":
		return plugin.importCodex(ctx, host, body)
	case accountPrefix + "refresh-web":
		return plugin.refreshWeb(ctx, host, body)
	case accountPrefix + "set-web-enabled":
		return plugin.setWebEnabled(ctx, host, body)
	case accountPrefix + "webaccounts":
		if _, errEntries := host.entries(ctx); errEntries != nil {
			return httpFailure(errEntries.HTTPStatus, errEntries.Code)
		}
		api, errAPI := plugin.webAPI()
		if errAPI != nil {
			return httpFailure(errAPI.HTTPStatus, errAPI.Code)
		}
		snapshot, errSnapshot := api.accounts(ctx)
		if errSnapshot != nil {
			return httpFailure(errSnapshot.HTTPStatus, errSnapshot.Code)
		}
		now := plugin.now().UTC()
		views := make([]safeWebAccountView, 0, len(snapshot.Items))
		for _, account := range snapshot.Items {
			views = append(views, account.view(now))
		}
		return accountJSON(struct {
			Accounts     []safeWebAccountView   `json:"accounts"`
			ObservedAt   string                 `json:"observed_at"`
			Source       string                 `json:"source"`
			Capabilities webAccountCapabilities `json:"capabilities"`
		}{views, now.Format(time.RFC3339Nano), "ChatGPTWeb", webAccountCapabilities{snapshot.preservesDisabled()}})
	default:
		return httpFailure(404, "not_found")
	}
}

func (plugin *service) importCodex(ctx context.Context, host scopedHost, body []byte) (json.RawMessage, *publicError) {
	var input struct {
		ID                  string `json:"id"`
		Consent             bool   `json:"consent"`
		AllowDisabledSource bool   `json:"allow_disabled_source,omitempty"`
	}
	if !decodeAccountInput(body, &input) || !input.Consent || !validOpaqueID(input.ID, "codex") {
		return httpFailure(400, "invalid_import_request_require_consent")
	}
	credential, err := host.credential(ctx, sourceID(input.ID), input.AllowDisabledSource)
	if err != nil {
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
	for _, account := range snapshot.Items {
		if account.AccessToken == credential.value {
			return accountJSON(struct {
				Status  string             `json:"status"`
				Account safeWebAccountView `json:"account"`
			}{"already_present", account.view(plugin.now().UTC())})
		}
	}
	if !snapshot.preservesDisabled() {
		return httpFailure(409, "vendor_disabled_import_unsupported")
	}
	type disabledAccount struct {
		AccessToken string `json:"access_token"`
		Status      string `json:"status"`
		SourceType  string `json:"source_type"`
	}
	requestBody, errEncode := json.Marshal(struct {
		PreserveDisabled bool              `json:"preserve_disabled"`
		Accounts         []disabledAccount `json:"accounts"`
	}{true, []disabledAccount{{credential.value, vendorDisabled, "codex"}}})
	if errEncode != nil {
		return httpFailure(500, "request_encoding_failed")
	}
	raw, errImport := api.request(ctx, http.MethodPost, "/api/accounts", requestBody)
	if errImport != nil {
		return httpFailure(errImport.HTTPStatus, errImport.Code)
	}
	imported, errImported := parseSnapshot(raw)
	if errImported != nil {
		return httpFailure(errImported.HTTPStatus, errImported.Code)
	}
	var result struct {
		Added *int `json:"added"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Added == nil || (*result.Added != 0 && *result.Added != 1) {
		return httpFailure(502, "web_api_invalid_response")
	}
	for _, account := range imported.Items {
		if account.AccessToken != credential.value {
			continue
		}
		status := "already_present"
		if *result.Added == 1 {
			if account.Status != vendorDisabled {
				return httpFailure(502, "vendor_disabled_contract_violated")
			}
			status = "imported_disabled"
		}
		return accountJSON(struct {
			Status  string             `json:"status"`
			Account safeWebAccountView `json:"account"`
		}{status, account.view(plugin.now().UTC())})
	}
	return httpFailure(409, "web_account_stale_reload_needed")
}
