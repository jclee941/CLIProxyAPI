package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const codexFixtureToken = "synthetic-access-marker"

func codexManagement(t *testing.T, plugin *service, method, path, body, callbackID string) httpResponse {
	t.Helper()
	raw, err := json.Marshal(struct {
		Method     string
		Path       string
		Body       []byte
		CallbackID string `json:"host_callback_id"`
	}{method, "/v0/management/plugins/chatgpt2api/" + path, []byte(body), callbackID})
	if err != nil {
		t.Fatal(err)
	}
	response := invoke(t, plugin, "management.handle", raw)
	var result httpResponse
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{codexFixtureToken, "synthetic-id-marker", "synthetic-refresh-marker", "private-metadata-marker", "synthetic-admin-marker"} {
		if strings.Contains(string(result.Body), marker) {
			t.Fatal("public credential or metadata leak")
		}
	}
	return result
}

func codexVendorFixture(t *testing.T, handler http.HandlerFunc) *service {
	t.Helper()
	t.Setenv("CHATGPT2API_AUTH_KEY", "synthetic-admin-marker")
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	plugin := configured(t)
	plugin.now = func() time.Time { return time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC) }
	response := invoke(t, plugin, "plugin.reconfigure", registrationInput(t, "model_names: [gpt-test]\napi_base_url: "+server.URL))
	if !response.OK {
		t.Fatal("fixture configuration failed")
	}
	t.Cleanup(plugin.accountClient.CloseIdleConnections)
	plugin.bindHost(func(ctx context.Context, method string, raw []byte) ([]byte, error) {
		return []byte(`{"ok":true,"result":{"files":[]}}`), nil
	})
	return plugin
}

func codexWriteFixture(t *testing.T, writer http.ResponseWriter, raw string) {
	t.Helper()
	if _, err := io.WriteString(writer, raw); err != nil {
		t.Error(err)
	}
}

func codexBindFixture(t *testing.T, plugin *service, entry, storage string) *atomic.Int64 {
	t.Helper()
	calls := new(atomic.Int64)
	plugin.bindHost(func(ctx context.Context, method string, raw []byte) ([]byte, error) {
		calls.Add(1)
		var request struct {
			CallbackID string `json:"host_callback_id"`
			Index      string `json:"auth_index"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if request.CallbackID != "fresh-scope" {
			return nil, fmt.Errorf("private-metadata-marker")
		}
		var result string
		switch method {
		case "host.auth.list":
			result = `{"files":[` + entry + `]}`
		case "host.auth.get_runtime":
			if request.Index != "physical-index" {
				return nil, fmt.Errorf("unexpected index")
			}
			result = `{"auth":` + entry + `}`
		case "host.auth.get":
			if request.Index != "physical-index" {
				return nil, fmt.Errorf("unexpected index")
			}
			result = `{"auth_index":"physical-index","name":"physical.json","json":` + storage + `}`
		default:
			return nil, fmt.Errorf("forbidden mutation callback")
		}
		return []byte(`{"ok":true,"result":` + result + `}`), nil
	})
	return calls
}

const codexPhysicalFixture = `{"id":"physical.json","auth_index":"physical-index","name":"physical.json","provider":"codex","type":"codex","source":"file","disabled":true,"label":"private-metadata-marker"}`
const codexStorageFixture = `{"type":"codex","access_token":"synthetic-access-marker","id_token":"synthetic-id-marker","refresh_token":"synthetic-refresh-marker","disabled":true}`

func codexListedID(t *testing.T, plugin *service) string {
	t.Helper()
	response := codexManagement(t, plugin, "GET", "codex-sources", "", "fresh-scope")
	var result struct {
		Sources []struct {
			ID, Label, Provider string
			Disabled            bool
		}
	}
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || len(result.Sources) != 1 || !result.Sources[0].Disabled || result.Sources[0].Provider != "codex" {
		t.Fatalf("source listing failed: %s", response.Body)
	}
	return result.Sources[0].ID
}

func TestCodexImport_whenExactTokenExists_doesNotPostOrChangeRefreshOwner(t *testing.T) {
	var posts atomic.Int64
	plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != "GET" {
			posts.Add(1)
		}
		codexWriteFixture(t, writer, `{"items":[{"access_token":"synthetic-access-marker","refresh_token":"synthetic-refresh-marker","status":"正常","source_type":"web","quota":3,"email":"private-metadata-marker"}]}`)
	})
	codexBindFixture(t, plugin, codexPhysicalFixture, codexStorageFixture)
	id := codexListedID(t, plugin)

	response := codexManagement(t, plugin, "POST", "import-codex", `{"id":"`+id+`","consent":true,"allow_disabled_source":true}`, "fresh-scope")

	if response.StatusCode != 200 || !strings.Contains(string(response.Body), `"status":"already_present"`) || posts.Load() != 0 {
		t.Fatalf("duplicate contract: %s", response.Body)
	}
	if !strings.Contains(string(response.Body), `"disabled":false`) || !strings.Contains(string(response.Body), `"source_type":"web"`) {
		t.Fatal("existing account was changed")
	}
}

func TestCodexImport_whenNewToken_vendorDisabledPreflightBlockPreventsAllWrites(t *testing.T) {
	var posts atomic.Int64
	plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != "GET" {
			posts.Add(1)
		}
		codexWriteFixture(t, writer, `{"items":[{"access_token":"another-synthetic-token","status":"正常","email":"same-email"}]}`)
	})
	codexBindFixture(t, plugin, codexPhysicalFixture, codexStorageFixture)
	id := codexListedID(t, plugin)

	response := codexManagement(t, plugin, "POST", "import-codex", `{"id":"`+id+`","consent":true,"allow_disabled_source":true}`, "fresh-scope")

	if response.StatusCode != 409 || string(response.Body) != `{"error":"vendor_disabled_import_unsupported"}` || posts.Load() != 0 {
		t.Fatalf("unsafe import: %s", response.Body)
	}
}

func TestCodexImport_whenConsentOrSourcePolicyInvalid_neverCallsVendor(t *testing.T) {
	for _, scenario := range []struct {
		name, entry, storage, body string
		status                     int
	}{
		{"no-consent", codexPhysicalFixture, codexStorageFixture, `{"id":"ID"}`, 400},
		{"false-consent", codexPhysicalFixture, codexStorageFixture, `{"id":"ID","consent":false,"allow_disabled_source":true}`, 400},
		{"disabled", codexPhysicalFixture, codexStorageFixture, `{"id":"ID","consent":true}`, 409},
		{"path", codexPhysicalFixture, codexStorageFixture, `{"id":"../../physical.json","consent":true}`, 400},
		{"blob", codexPhysicalFixture, codexStorageFixture, `{"id":"ID","consent":true,"access_token":"client-blob"}`, 400},
		{"type", codexPhysicalFixture, `{"type":"other","access_token":"synthetic-access-marker"}`, `{"id":"ID","consent":true,"allow_disabled_source":true}`, 409},
		{"missing-access", codexPhysicalFixture, `{"type":"codex"}`, `{"id":"ID","consent":true,"allow_disabled_source":true}`, 409},
		{"access-type", codexPhysicalFixture, `{"type":"codex","access_token":42}`, `{"id":"ID","consent":true,"allow_disabled_source":true}`, 409},
		{"crlf", codexPhysicalFixture, `{"type":"codex","access_token":"bad\r\nvalue"}`, `{"id":"ID","consent":true,"allow_disabled_source":true}`, 409},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var calls atomic.Int64
			plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				codexWriteFixture(t, writer, `{"items":[]}`)
			})
			codexBindFixture(t, plugin, scenario.entry, scenario.storage)
			id := codexListedID(t, plugin)

			response := codexManagement(t, plugin, "POST", "import-codex", strings.ReplaceAll(scenario.body, "ID", id), "fresh-scope")

			if response.StatusCode != scenario.status || calls.Load() != 0 {
				t.Fatalf("policy failed: status=%d body=%s calls=%d", response.StatusCode, response.Body, calls.Load())
			}
		})
	}
}

func TestWebAccounts_whenCredentialRichRecords_returnsOnlySafeRealMetrics(t *testing.T) {
	plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != "GET" || request.URL.Path != "/api/accounts" || request.Header.Get("Authorization") != "Bearer synthetic-admin-marker" {
			t.Error("wrong private inventory request")
		}
		codexWriteFixture(t, writer, `{"items":[{"access_token":"synthetic-access-marker","refresh_token":"synthetic-refresh-marker","id_token":"synthetic-id-marker","status":"禁用","source_type":"codex","type":"Pro","quota":7,"limits_progress":[{"feature_name":"image_gen","remaining":9,"reset_after":"2026-09-13T00:02:00Z"}],"email":"private-metadata-marker","last_refresh_error":"synthetic-access-marker"},{"access_token":"other-token","status":"正常","quota":null,"limits_progress":[{"feature_name":"image_gen","remaining":"bad","reset_after":-1}]}]}`)
	})

	response := codexManagement(t, plugin, "GET", "webaccounts", "", "fresh-scope")

	var result struct {
		Accounts           []safeWebAccountView
		ObservedAt, Source string
	}
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || result.Source != "ChatGPTWeb" || len(result.Accounts) != 2 {
		t.Fatalf("inventory failed: %s", response.Body)
	}
	first := result.Accounts[0]
	if !first.Disabled || first.TrackedImageRemaining == nil || *first.TrackedImageRemaining != 7 || first.ObservedImageRemaining == nil || *first.ObservedImageRemaining != 9 || first.ResetAfterSeconds == nil || *first.ResetAfterSeconds != 120 || first.RefreshError == nil {
		t.Fatal("real metrics missing")
	}
	if result.Accounts[1].TrackedImageRemaining != nil || result.Accounts[1].ObservedImageRemaining != nil || result.Accounts[1].ResetAfterSeconds != nil {
		t.Fatal("invented quota")
	}
}
