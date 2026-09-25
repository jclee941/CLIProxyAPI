package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSetWebEnabled_whenStateAlreadyRequested_returnsUnchangedSnapshotWithoutPost(t *testing.T) {
	for _, scenario := range []struct{ status, enabled, publicStatus string }{
		{vendorDisabled, "false", "disabled"},
		{"\u6b63\u5e38", "true", "normal"},
		{"\u9650\u6d41", "true", "limited"},
		{"\u5f02\u5e38", "true", "abnormal"},
	} {
		t.Run(scenario.publicStatus, func(t *testing.T) {
			var posts atomic.Int64
			plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet {
					posts.Add(1)
				}
				codexWriteFixture(t, writer, `{"items":[{"access_token":"synthetic-access-marker","status":"`+scenario.status+`","quota":0,"last_refresh_error":"synthetic-refresh-marker"}],"capabilities":{"preserve_disabled_accounts":1}}`)
			})

			response := codexManagement(t, plugin, "POST", "set-web-enabled", `{"id":"`+opaqueID("web", codexFixtureToken)+`","enabled":`+scenario.enabled+`,"consent":true}`, "fresh-scope")

			var result struct{ Account safeWebAccountView }
			if response.StatusCode != 200 || json.Unmarshal(response.Body, &result) != nil || posts.Load() != 0 || result.Account.Status != scenario.publicStatus || result.Account.RefreshError == nil || result.Account.ObservedAt != nil {
				t.Fatalf("no-op changed observed state: %s", response.Body)
			}
		})
	}
}

func TestSetWebEnabled_whenInventoryCannotIdentifySafeTarget_neverPosts(t *testing.T) {
	for _, scenario := range []struct {
		name, items, code string
		status            int
	}{
		{"missing", `[]`, "web_account_stale_reload_needed", 409},
		{"rotated", `[{"access_token":"rotated-snapshot","email":"private-metadata-marker","status":"\u7981\u7528"}]`, "web_account_stale_reload_needed", 409},
		{"unknown-state", `[{"access_token":"synthetic-access-marker","status":"private-metadata-marker"}]`, "web_account_status_unknown", 409},
		{"duplicate-token", `[{"access_token":"synthetic-access-marker","status":"\u7981\u7528"},{"access_token":"synthetic-access-marker","status":"\u7981\u7528"}]`, "web_api_invalid_response", 502},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var posts atomic.Int64
			plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet {
					posts.Add(1)
				}
				codexWriteFixture(t, writer, `{"items":`+scenario.items+`,"capabilities":{"preserve_disabled_accounts":1}}`)
			})

			response := codexManagement(t, plugin, "POST", "set-web-enabled", `{"id":"`+opaqueID("web", codexFixtureToken)+`","enabled":true,"consent":true}`, "fresh-scope")

			if response.StatusCode != scenario.status || string(response.Body) != `{"error":"`+scenario.code+`"}` || posts.Load() != 0 {
				t.Fatalf("unsafe inventory used: %s", response.Body)
			}
		})
	}
}

func TestSetWebEnabled_whenUpdateCannotConfirmState_returnsFixedErrorWithoutRetry(t *testing.T) {
	const normal = `{"access_token":"synthetic-access-marker","status":"\u6b63\u5e38"}`
	const disabled = `{"access_token":"synthetic-access-marker","status":"\u7981\u7528"}`
	const rotated = `{"access_token":"rotated-snapshot","status":"\u6b63\u5e38","email":"private-metadata-marker"}`
	for _, scenario := range []struct {
		name, raw, code      string
		vendorStatus, status int
	}{
		{"http-error", `{"error":"synthetic-access-marker"}`, "web_api_http_error", 500, 502},
		{"redirect", `{"error":"synthetic-refresh-marker"}`, "web_api_redirect_blocked", 307, 502},
		{"malformed", `synthetic-access-marker`, "web_api_invalid_response", 200, 502},
		{"oversized", strings.Repeat("x", accountBodyLimit+1), "web_api_response_too_large", 200, 502},
		{"no-item", `{"items":[` + normal + `]}`, "web_api_invalid_response", 200, 502},
		{"null-item", `{"item":null,"items":[` + normal + `]}`, "web_api_invalid_response", 200, 502},
		{"rotated-item", `{"item":` + rotated + `,"items":[` + normal + `]}`, "web_account_stale_reload_needed", 200, 409},
		{"rotated-inventory", `{"item":` + normal + `,"items":[` + rotated + `]}`, "web_account_stale_reload_needed", 200, 409},
		{"missing-snapshot", `{"item":` + normal + `,"items":[]}`, "web_account_stale_reload_needed", 200, 409},
		{"duplicate", `{"item":` + normal + `,"items":[` + normal + `,` + normal + `]}`, "web_api_invalid_response", 200, 502},
		{"item-contradiction", `{"item":` + disabled + `,"items":[` + normal + `]}`, "vendor_web_enabled_contract_violated", 200, 502},
		{"inventory-contradiction", `{"item":` + normal + `,"items":[` + disabled + `]}`, "vendor_web_enabled_contract_violated", 200, 502},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var posts atomic.Int64
			plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
				if request.Method == http.MethodGet {
					codexWriteFixture(t, writer, `{"items":[`+disabled+`],"capabilities":{"preserve_disabled_accounts":1}}`)
					return
				}
				posts.Add(1)
				writer.Header().Set("Location", "/forbidden")
				writer.WriteHeader(scenario.vendorStatus)
				codexWriteFixture(t, writer, scenario.raw)
			})

			response := codexManagement(t, plugin, "POST", "set-web-enabled", `{"id":"`+opaqueID("web", codexFixtureToken)+`","enabled":true,"consent":true}`, "fresh-scope")

			if response.StatusCode != scenario.status || string(response.Body) != `{"error":"`+scenario.code+`"}` || posts.Load() != 1 {
				t.Fatalf("unconfirmed state reported or retried: status=%d body=%s posts=%d", response.StatusCode, response.Body, posts.Load())
			}
		})
	}
}
