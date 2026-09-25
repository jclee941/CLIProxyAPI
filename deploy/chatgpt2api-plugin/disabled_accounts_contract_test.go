package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDisabledImport_whenCapabilityVerified_postsOnlyAccessAndReturnsSafeView(t *testing.T) {
	for _, added := range []int{0, 1} {
		t.Run(map[int]string{0: "concurrent-duplicate", 1: "new"}[added], func(t *testing.T) {
			var posts atomic.Int64
			plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/api/accounts" || request.Header.Get("Authorization") != "Bearer synthetic-admin-marker" {
					t.Error("unexpected private request")
				}
				if request.Method == http.MethodGet {
					codexWriteFixture(t, writer, `{"items":[],"capabilities":{"preserve_disabled_accounts":1}}`)
					return
				}
				posts.Add(1)
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
				}
				var payload struct {
					PreserveDisabled bool                `json:"preserve_disabled"`
					Accounts         []map[string]string `json:"accounts"`
				}
				if request.Method != http.MethodPost || json.Unmarshal(body, &payload) != nil || !payload.PreserveDisabled || len(payload.Accounts) != 1 {
					t.Error("protected import contract missing")
					writer.WriteHeader(400)
					return
				}
				account := payload.Accounts[0]
				if len(account) != 3 || account["access_token"] != codexFixtureToken || account["status"] != vendorDisabled || account["source_type"] != "codex" {
					t.Error("unexpected credential forwarding or initial status")
				}
				status := vendorDisabled
				if added == 0 {
					status = "正常"
				}
				response := `{"added":1,"items":[{"access_token":"synthetic-access-marker","status":"` + status + `","source_type":"codex","quota":8,"refresh_token":"synthetic-refresh-marker","email":"private-metadata-marker"}]}`
				if added == 0 {
					response = strings.Replace(response, `"added":1`, `"added":0`, 1)
				}
				codexWriteFixture(t, writer, response)
			})
			codexBindFixture(t, plugin, codexPhysicalFixture, codexStorageFixture)
			id := codexListedID(t, plugin)

			response := codexManagement(t, plugin, "POST", "import-codex", `{"id":"`+id+`","consent":true,"allow_disabled_source":true}`, "fresh-scope")

			status := "imported_disabled"
			if added == 0 {
				status = "already_present"
			}
			if response.StatusCode != 200 || posts.Load() != 1 || !strings.Contains(string(response.Body), `"status":"`+status+`"`) {
				t.Fatalf("protected import failed: %s", response.Body)
			}
			var result struct{ Account safeWebAccountView }
			if err := json.Unmarshal(response.Body, &result); err != nil {
				t.Fatal(err)
			}
			if result.Account.Disabled != (added == 1) {
				t.Fatal("incorrect disabled DTO")
			}
		})
	}
}

func TestDisabledOperations_whenCapabilityAbsentOrUnknown_neverPost(t *testing.T) {
	for _, marker := range []string{`null`, `{}`, `{"preserve_disabled_accounts":0}`, `{"preserve_disabled_accounts":2}`, `{"preserve_disabled_accounts":true}`, `{"preserve_disabled_accounts":"1"}`} {
		t.Run(marker, func(t *testing.T) {
			var posts atomic.Int64
			plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet {
					posts.Add(1)
				}
				codexWriteFixture(t, writer, `{"items":[{"access_token":"other-disabled-token","status":"禁用"}],"capabilities":`+marker+`}`)
			})
			codexBindFixture(t, plugin, codexPhysicalFixture, codexStorageFixture)
			id := codexListedID(t, plugin)
			imported := codexManagement(t, plugin, "POST", "import-codex", `{"id":"`+id+`","consent":true,"allow_disabled_source":true}`, "fresh-scope")
			refreshed := codexManagement(t, plugin, "POST", "refresh-web", `{"id":"`+opaqueID("web", "other-disabled-token")+`"}`, "fresh-scope")
			if imported.StatusCode != 409 || refreshed.StatusCode != 409 || posts.Load() != 0 {
				t.Fatal("unsupported vendor received a mutation")
			}
		})
	}
}

func TestDisabledRefresh_whenCapabilityVerified_preservesStatusAndObservedQuota(t *testing.T) {
	var posts atomic.Int64
	plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/accounts":
			codexWriteFixture(t, writer, `{"items":[{"access_token":"synthetic-access-marker","status":"禁用","quota":3}],"capabilities":{"preserve_disabled_accounts":1}}`)
		case "/api/accounts/refresh":
			posts.Add(1)
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Error(err)
			}
			if string(body) != `{"access_tokens":["synthetic-access-marker"],"preserve_disabled":true}` {
				t.Error("preservation contract not sent")
			}
			codexWriteFixture(t, writer, `{"progress_id":"00112233-4455-6677-8899-aabbccddeeff"}`)
		case "/api/accounts/refresh/progress/00112233-4455-6677-8899-aabbccddeeff":
			codexWriteFixture(t, writer, `{"done":true,"result":{"refreshed":1,"errors":[],"items":[{"access_token":"synthetic-access-marker","status":"禁用","quota":4,"limits_progress":[{"feature_name":"image_gen","remaining":7}]}]}}`)
		default:
			t.Error("unexpected vendor path")
		}
	})

	response := codexManagement(t, plugin, "POST", "refresh-web", `{"id":"`+opaqueID("web", codexFixtureToken)+`"}`, "fresh-scope")
	var result struct{ Account safeWebAccountView }
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || posts.Load() != 1 || !result.Account.Disabled || result.Account.ObservedAt == nil || result.Account.TrackedImageRemaining == nil || *result.Account.TrackedImageRemaining != 4 || result.Account.ObservedImageRemaining == nil || *result.Account.ObservedImageRemaining != 7 {
		t.Fatalf("disabled refresh failed: %s", response.Body)
	}
}

func TestWebAccounts_whenCapabilityAdvertised_returnsNonsecretReadiness(t *testing.T) {
	plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		codexWriteFixture(t, writer, `{"items":[],"capabilities":{"preserve_disabled_accounts":1,"private":"synthetic-access-marker"}}`)
	})
	response := codexManagement(t, plugin, "GET", "webaccounts", "", "fresh-scope")
	if response.StatusCode != 200 || !strings.Contains(string(response.Body), `"capabilities":{"preserve_disabled_accounts":true}`) {
		t.Fatalf("missing readiness: %s", response.Body)
	}
}

func TestDisabledImport_whenVendorViolatesContract_neverReportsSuccess(t *testing.T) {
	plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			codexWriteFixture(t, writer, `{"items":[],"capabilities":{"preserve_disabled_accounts":1}}`)
			return
		}
		codexWriteFixture(t, writer, `{"added":1,"items":[{"access_token":"synthetic-access-marker","status":"正常"}]}`)
	})
	codexBindFixture(t, plugin, codexPhysicalFixture, codexStorageFixture)
	id := codexListedID(t, plugin)
	response := codexManagement(t, plugin, "POST", "import-codex", `{"id":"`+id+`","consent":true,"allow_disabled_source":true}`, "fresh-scope")
	if response.StatusCode != 502 || string(response.Body) != `{"error":"vendor_disabled_contract_violated"}` {
		t.Fatal("unsafe vendor result reported as imported")
	}
}

func TestDisabledImport_whenSourceEnabled_stillCreatesDisabledWithoutOverride(t *testing.T) {
	plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			codexWriteFixture(t, writer, `{"items":[],"capabilities":{"preserve_disabled_accounts":1}}`)
			return
		}
		var input struct{ Accounts []struct{ Status string } }
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if len(input.Accounts) != 1 || input.Accounts[0].Status != vendorDisabled {
			t.Error("source enablement copied to target")
		}
		codexWriteFixture(t, writer, `{"added":1,"items":[{"access_token":"synthetic-access-marker","status":"禁用"}]}`)
	})
	codexBindFixture(t, plugin, strings.ReplaceAll(codexPhysicalFixture, `"disabled":true`, `"disabled":false`), strings.ReplaceAll(codexStorageFixture, `"disabled":true`, `"disabled":false`))
	listed := codexManagement(t, plugin, "GET", "codex-sources", "", "fresh-scope")
	var sources struct{ Sources []codexSourceView }
	if err := json.Unmarshal(listed.Body, &sources); err != nil {
		t.Fatal(err)
	}
	if len(sources.Sources) != 1 || sources.Sources[0].Disabled {
		t.Fatal("enabled source unavailable")
	}
	response := codexManagement(t, plugin, "POST", "import-codex", `{"id":"`+string(sources.Sources[0].ID)+`","consent":true}`, "fresh-scope")
	if response.StatusCode != 200 || !strings.Contains(string(response.Body), `"disabled":true`) {
		t.Fatalf("enabled source import failed: %s", response.Body)
	}
}
