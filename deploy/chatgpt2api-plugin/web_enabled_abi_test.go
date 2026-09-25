//go:build abismoke

package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCABI_SetWebEnabled_whenExplicitAction_roundTripsSafeStateAndRejectsBadInput(t *testing.T) {
	var posts atomic.Int64
	plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Path == "/api/accounts" {
			codexWriteFixture(t, writer, `{"items":[{"access_token":"synthetic-access-marker","status":"\u7981\u7528"}],"capabilities":{"preserve_disabled_accounts":1}}`)
			return
		}
		posts.Add(1)
		var payload map[string]string
		if request.Method != http.MethodPost || request.URL.Path != "/api/accounts/update" || json.NewDecoder(request.Body).Decode(&payload) != nil || len(payload) != 2 || payload["access_token"] != codexFixtureToken || payload["status"] != "\u6b63\u5e38" {
			t.Error("wrong ABI status-only update")
		}
		codexWriteFixture(t, writer, `{"item":{"access_token":"synthetic-access-marker","status":"\u6b63\u5e38"},"items":[{"access_token":"synthetic-access-marker","status":"\u6b63\u5e38","refresh_token":"synthetic-refresh-marker","quota":0,"last_refresh_error":"synthetic-id-marker"}]}`)
	})
	previous := pluginService
	pluginService = plugin
	t.Cleanup(func() { pluginService = previous })
	if !initializeCodexCallbackFixture() {
		t.Fatal("native host initialization failed")
	}
	for _, scenario := range []struct {
		name, body, scope string
		status            int
	}{
		{"enable", `{"id":"ID","enabled":true,"consent":true}`, "fresh-scope", 200},
		{"missing-consent", `{"id":"ID","enabled":true}`, "fresh-scope", 400},
		{"stale-scope", `{"id":"ID","enabled":true,"consent":true}`, "old-scope", 503},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request, err := json.Marshal(struct {
				Method, Path string
				Body         []byte
				Scope        string `json:"host_callback_id"`
			}{"POST", accountPrefix + "set-web-enabled", []byte(strings.ReplaceAll(scenario.body, "ID", opaqueID("web", codexFixtureToken))), scenario.scope})
			if err != nil {
				t.Fatal(err)
			}

			raw, errCall := nativeFixtureCall("management.handle", request)

			if errCall != nil {
				t.Fatal(errCall)
			}
			var response envelope
			var result httpResponse
			if json.Unmarshal(raw, &response) != nil || !response.OK || json.Unmarshal(response.Result, &result) != nil || result.StatusCode != scenario.status || strings.Contains(string(result.Body), "marker") {
				t.Fatal("ABI response contract failed")
			}
			if scenario.status == 200 {
				var view struct{ Account safeWebAccountView }
				if json.Unmarshal(result.Body, &view) != nil || view.Account.Disabled || view.Account.Status != "normal" || view.Account.ObservedAt != nil || view.Account.RefreshError == nil {
					t.Fatal("ABI state projection failed")
				}
			}
		})
	}
	calls, frees := codexCallbackFixtureCounts()
	if posts.Load() != 1 || calls != 1 || frees != calls {
		t.Fatalf("ABI callback/mutation ownership failed: posts=%d calls=%d frees=%d", posts.Load(), calls, frees)
	}
}
