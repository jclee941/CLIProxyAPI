package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWebRefresh_whenEnabled_tracksSelectedSnapshotAndSanitizesProgress(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "rotation", "disabled"} {
		t.Run(outcome, func(t *testing.T) {
			var posts atomic.Int64
			plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/api/accounts":
					status := "正常"
					if outcome == "disabled" {
						status = "禁用"
					}
					codexWriteFixture(t, writer, `{"items":[{"access_token":"synthetic-access-marker","status":"`+status+`","quota":4}]}`)
				case "/api/accounts/refresh":
					posts.Add(1)
					body, err := io.ReadAll(request.Body)
					if err != nil {
						t.Error(err)
					}
					if request.Method != "POST" || string(body) != `{"access_tokens":["synthetic-access-marker"]}` {
						t.Error("wrong refresh selection")
					}
					codexWriteFixture(t, writer, `{"progress_id":"00112233-4455-6677-8899-aabbccddeeff"}`)
				case "/api/accounts/refresh/progress/00112233-4455-6677-8899-aabbccddeeff":
					token := codexFixtureToken
					if outcome == "rotation" {
						token = "rotated-synthetic-token"
					}
					errors := `[]`
					refreshed := "1"
					if outcome == "failure" {
						errors = `[{"token":"synthetic-access-marker","error":"private-metadata-marker"}]`
						refreshed = "0"
					}
					codexWriteFixture(t, writer, `{"done":true,"result":{"refreshed":`+refreshed+`,"errors":`+errors+`,"items":[{"access_token":"`+token+`","status":"正常","quota":6,"limits_progress":[{"feature_name":"image_gen","remaining":6,"reset_after":"2026-09-13T00:02:00Z"}]}]}}`)
				default:
					t.Error("unexpected vendor path")
					writer.WriteHeader(404)
				}
			})
			id := opaqueID("web", codexFixtureToken)

			response := codexManagement(t, plugin, "POST", "refresh-web", `{"id":"`+id+`"}`, "fresh-scope")

			if outcome == "disabled" {
				if response.StatusCode != 409 || posts.Load() != 0 || !strings.Contains(string(response.Body), "vendor_disabled_refresh_unsupported") {
					t.Fatal("disabled refresh could enable target")
				}
				return
			}
			if posts.Load() != 1 {
				t.Fatal("refresh must start exactly once")
			}
			if outcome == "rotation" {
				if response.StatusCode != 409 || !strings.Contains(string(response.Body), "web_account_stale_reload_needed") {
					t.Fatal("rotation guessed identity")
				}
				return
			}
			var result struct {
				Account            safeWebAccountView
				Source, ObservedAt string
			}
			if err := json.Unmarshal(response.Body, &result); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 200 || result.Source != "ChatGPTWeb" {
				t.Fatalf("refresh failed: %s", response.Body)
			}
			if outcome == "failure" {
				if result.Account.RefreshError == nil || *result.Account.RefreshError != "vendor_refresh_failed" || result.Account.ObservedAt != nil {
					t.Fatal("failure incorrectly presented as fresh")
				}
			} else if result.Account.ObservedAt == nil || result.Account.ObservationSource != "conversation/init" || result.Account.ObservedImageRemaining == nil || *result.Account.ObservedImageRemaining != 6 {
				t.Fatal("fresh quota missing")
			}
		})
	}
}

func TestWebAccounts_whenUnconfiguredOrUnsafeVendor_sanitizesErrors(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		status   int
		body     string
		expected string
	}{
		{"http", 401, `synthetic-access-marker`, "web_api_http_error"},
		{"redirect", 307, `synthetic-access-marker`, "web_api_redirect_blocked"},
		{"invalid", 200, `{"items":"synthetic-access-marker"}`, "web_api_invalid_response"},
		{"oversized", 200, strings.Repeat("x", accountBodyLimit+1), "web_api_response_too_large"},
		{"missing-key", 200, `{"items":[]}`, "web_api_unconfigured_set_CHATGPT2API_AUTH_KEY"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var calls atomic.Int64
			plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				writer.Header().Set("Location", "http://127.0.0.1:1/credential-leak")
				writer.WriteHeader(scenario.status)
				codexWriteFixture(t, writer, scenario.body)
			})
			if scenario.name == "missing-key" {
				t.Setenv("CHATGPT2API_AUTH_KEY", "")
			}

			response := codexManagement(t, plugin, "GET", "webaccounts", "", "fresh-scope")

			if !strings.Contains(string(response.Body), scenario.expected) {
				t.Fatalf("unsafe error: %s", response.Body)
			}
			if scenario.name == "missing-key" && calls.Load() != 0 {
				t.Fatal("unconfigured request was sent")
			}
		})
	}
}
