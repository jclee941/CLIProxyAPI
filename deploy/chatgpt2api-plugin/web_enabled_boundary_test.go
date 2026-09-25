package main

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSetWebEnabled_whenInputInvalid_neverCallsHostOrVendor(t *testing.T) {
	for _, body := range []string{
		`{}`, `null`, `[]`, ``,
		`{"id":"ID","enabled":true}`, `{"id":"ID","enabled":true,"consent":false}`,
		`{"id":"ID","enabled":true,"consent":null}`, `{"id":"ID","enabled":true,"consent":"true"}`,
		`{"id":"ID","consent":true}`, `{"id":"ID","enabled":null,"consent":true}`,
		`{"id":"ID","enabled":"false","consent":true}`, `{"id":"ID","enabled":0,"consent":true}`,
		`{"enabled":true,"consent":true}`, `{"id":null,"enabled":true,"consent":true}`,
		`{"id":"../source.json","enabled":true,"consent":true}`,
		`{"id":"ID","enabled":true,"consent":true,"access_token":"synthetic-access-marker"}`,
		`{"id":"ID","enabled":true,"consent":true,"url":"https://invalid.example"}`,
		`{"id":"ID","enabled":true,"consent":true,"allow_disabled_source":true}`,
		`{"id":"ID","Enabled":true,"consent":true}`,
		`{"id":"ID","enabled":false,"enabled":true,"consent":true}`,
		`{"id":"ID","enabled":true,"consent":false,"consent":true}`,
		`{"id":"ID","id":"ID","enabled":true,"consent":true}`,
		`{"id":"ID","enabled":true,"consent":true}{}`,
		strings.Repeat(" ", 4097) + `{}`,
	} {
		t.Run(body, func(t *testing.T) {
			var requests atomic.Int64
			plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				writer.WriteHeader(500)
			})
			calls := codexBindFixture(t, plugin, codexPhysicalFixture, codexStorageFixture)

			response := codexManagement(t, plugin, "POST", "set-web-enabled", strings.ReplaceAll(body, "ID", opaqueID("web", codexFixtureToken)), "fresh-scope")

			if response.StatusCode != 400 || requests.Load() != 0 || calls.Load() != 0 {
				t.Fatalf("invalid input accepted: status=%d requests=%d calls=%d", response.StatusCode, requests.Load(), calls.Load())
			}
		})
	}
}

func TestSetWebEnabled_whenScopeOrKeyUnavailable_neverCallsVendor(t *testing.T) {
	for _, scenario := range []struct{ name, scope, key, code string }{
		{"missing-scope", "", "synthetic-admin-marker", "host_callback_required"},
		{"stale-scope", "old-scope", "synthetic-admin-marker", "host_auth_unavailable"},
		{"missing-key", "fresh-scope", "", "web_api_unconfigured_set_CHATGPT2API_AUTH_KEY"},
		{"invalid-key", "fresh-scope", "invalid\r\nkey", "web_api_unconfigured_set_CHATGPT2API_AUTH_KEY"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var requests atomic.Int64
			plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				writer.WriteHeader(500)
			})
			codexBindFixture(t, plugin, codexPhysicalFixture, codexStorageFixture)
			t.Setenv("CHATGPT2API_AUTH_KEY", scenario.key)

			response := codexManagement(t, plugin, "POST", "set-web-enabled", `{"id":"`+opaqueID("web", codexFixtureToken)+`","enabled":true,"consent":true}`, scenario.scope)

			if response.StatusCode != 503 || string(response.Body) != `{"error":"`+scenario.code+`"}` || requests.Load() != 0 {
				t.Fatalf("scope/config boundary failed: %s", response.Body)
			}
		})
	}
}

func TestSetWebEnabled_whenCapabilityUnsupported_neverPosts(t *testing.T) {
	for _, marker := range []string{`null`, `{}`, `{"preserve_disabled_accounts":0}`, `{"preserve_disabled_accounts":2}`, `{"preserve_disabled_accounts":true}`, `{"preserve_disabled_accounts":"1"}`} {
		for _, enabled := range []string{"true", "false"} {
			t.Run(marker+enabled, func(t *testing.T) {
				var posts atomic.Int64
				plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
					if request.Method != http.MethodGet {
						posts.Add(1)
					}
					codexWriteFixture(t, writer, `{"items":[{"access_token":"synthetic-access-marker","status":"\u7981\u7528"}],"capabilities":`+marker+`}`)
				})

				response := codexManagement(t, plugin, "POST", "set-web-enabled", `{"id":"`+opaqueID("web", codexFixtureToken)+`","enabled":`+enabled+`,"consent":true}`, "fresh-scope")

				if response.StatusCode != 409 || string(response.Body) != `{"error":"vendor_web_enabled_unsupported"}` || posts.Load() != 0 {
					t.Fatalf("unsupported operation accepted: %s", response.Body)
				}
			})
		}
	}
}

func TestSetWebEnabled_whenMethodWrong_returnsPostAllow(t *testing.T) {
	plugin := configured(t)

	response := codexManagement(t, plugin, "GET", "set-web-enabled", "", "")

	if response.StatusCode != 405 || response.Headers.Get("Allow") != "POST" {
		t.Fatalf("wrong method contract: %+v", response)
	}
}
