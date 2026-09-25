package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestSetWebEnabled_whenSourceStaysDisabled_neverReadsOrWritesItsCredentials(t *testing.T) {
	plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			codexWriteFixture(t, writer, `{"items":[{"access_token":"synthetic-access-marker","status":"\u7981\u7528"}],"capabilities":{"preserve_disabled_accounts":1}}`)
			return
		}
		codexWriteFixture(t, writer, `{"item":{"access_token":"synthetic-access-marker","status":"\u6b63\u5e38"},"items":[{"access_token":"synthetic-access-marker","status":"\u6b63\u5e38"}]}`)
	})
	path := filepath.Join(t.TempDir(), "physical.json")
	if err := os.WriteFile(path, []byte(codexStorageFixture), 0400); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	plugin.bindHost(func(ctx context.Context, method string, raw []byte) ([]byte, error) {
		if method != "host.auth.list" {
			t.Error("source credential read or mutation attempted")
			return nil, nil
		}
		return []byte(`{"ok":true,"result":{"files":[` + codexPhysicalFixture + `]}}`), nil
	})

	response := codexManagement(t, plugin, "POST", "set-web-enabled", `{"id":"`+opaqueID("web", codexFixtureToken)+`","enabled":true,"consent":true}`, "fresh-scope")

	if response.StatusCode != 200 {
		t.Fatalf("disabled-source enable failed: %s", response.Body)
	}
	content, errRead := os.ReadFile(path)
	if errRead != nil {
		t.Fatal(errRead)
	}
	after, errStat := os.Stat(path)
	if errStat != nil {
		t.Fatal(errStat)
	}
	if string(content) != codexStorageFixture || before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("source bytes or file metadata changed")
	}
}

func TestSetWebEnabled_whenConsented_changesOnlySelectedStatus(t *testing.T) {
	for _, scenario := range []struct {
		name, before, after, enabled, status string
	}{
		{"enable", vendorDisabled, "\u6b63\u5e38", "true", "normal"},
		{"disable", "\u6b63\u5e38", vendorDisabled, "false", "disabled"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var posts atomic.Int64
			plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
				if request.Header.Get("Authorization") != "Bearer synthetic-admin-marker" {
					t.Error("private authorization missing")
				}
				if request.Method == http.MethodGet && request.URL.Path == "/api/accounts" {
					codexWriteFixture(t, writer, `{"items":[{"access_token":"synthetic-access-marker","status":"`+scenario.before+`"},{"access_token":"other-snapshot","status":"`+scenario.before+`"}],"capabilities":{"preserve_disabled_accounts":1}}`)
					return
				}
				posts.Add(1)
				var payload map[string]string
				if request.Method != http.MethodPost || request.URL.Path != "/api/accounts/update" || json.NewDecoder(request.Body).Decode(&payload) != nil || len(payload) != 2 || payload["access_token"] != codexFixtureToken || payload["status"] != scenario.after {
					t.Error("update must contain only selected token and status")
					writer.WriteHeader(400)
					return
				}
				account := `{"access_token":"synthetic-access-marker","status":"` + scenario.after + `","source_type":"codex","quota":7,"type":"Pro","refresh_token":"synthetic-refresh-marker","id_token":"synthetic-id-marker","email":"private-metadata-marker","last_refresh_error":"synthetic-access-marker"}`
				codexWriteFixture(t, writer, `{"item":`+account+`,"items":[`+account+`,{"access_token":"other-snapshot","status":"`+scenario.before+`"}]}`)
			})
			calls := codexBindFixture(t, plugin, codexPhysicalFixture, codexStorageFixture)

			response := codexManagement(t, plugin, "POST", "set-web-enabled", `{"id":"`+opaqueID("web", codexFixtureToken)+`","enabled":`+scenario.enabled+`,"consent":true}`, "fresh-scope")

			var result struct {
				Account    safeWebAccountView `json:"account"`
				Source     string             `json:"source"`
				ObservedAt string             `json:"observed_at"`
			}
			if response.StatusCode != 200 || posts.Load() != 1 || calls.Load() != 1 || json.Unmarshal(response.Body, &result) != nil {
				t.Fatalf("selected update failed: status=%d body=%s posts=%d host_calls=%d", response.StatusCode, response.Body, posts.Load(), calls.Load())
			}
			if result.Account.ID != webAccountID(opaqueID("web", codexFixtureToken)) || result.Account.Status != scenario.status || result.Account.Disabled != (scenario.enabled == "false") || result.Account.TrackedImageRemaining == nil || *result.Account.TrackedImageRemaining != 7 {
				t.Fatal("observed state or metadata incorrect")
			}
			if result.Source != "ChatGPTWeb" || result.ObservedAt != "2026-09-13T00:00:00Z" || result.Account.ObservedAt != nil || result.Account.ObservationSource != "stored_snapshot" || result.Account.RefreshError == nil || response.Headers.Get("Cache-Control") != "no-store" {
				t.Fatal("update falsely claimed fresh quota or cleared errors")
			}
		})
	}
}
