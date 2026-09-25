//go:build abismoke

package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCABI_CodexImport_whenScopedHostCallback_usesCBridgeAndPrivateHTTP(t *testing.T) {
	var calls atomic.Int64
	plugin := codexVendorFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Method != "GET" || request.URL.Path != "/api/accounts" {
			t.Error("unexpected ABI vendor mutation")
		}
		codexWriteFixture(t, writer, `{"items":[{"access_token":"synthetic-access-marker","refresh_token":"synthetic-refresh-marker","status":"禁用","source_type":"web"}]}`)
	})
	previous := pluginService
	pluginService = plugin
	t.Cleanup(func() { pluginService = previous })
	if !initializeCodexCallbackFixture() {
		t.Fatal("native host callback initialization failed")
	}
	call := func(method, path, body, scope string) httpResponse {
		t.Helper()
		request, err := json.Marshal(struct {
			Method, Path string
			Body         []byte
			Scope        string `json:"host_callback_id"`
		}{method, accountPrefix + path, []byte(body), scope})
		if err != nil {
			t.Fatal(err)
		}
		raw, errCall := nativeFixtureCall("management.handle", request)
		if errCall != nil {
			t.Fatal(errCall)
		}
		var response envelope
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatal(err)
		}
		var result httpResponse
		if err := json.Unmarshal(response.Result, &result); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(result.Body), "marker") {
			t.Fatal("ABI public credential leak")
		}
		return result
	}
	listed := call("GET", "codex-sources", "", "fresh-scope")
	var sources struct{ Sources []codexSourceView }
	if err := json.Unmarshal(listed.Body, &sources); err != nil {
		t.Fatal(err)
	}
	if listed.StatusCode != 200 || len(sources.Sources) != 1 {
		t.Fatal("C host sources not listed")
	}
	imported := call("POST", "import-codex", `{"id":"`+string(sources.Sources[0].ID)+`","consent":true,"allow_disabled_source":true}`, "fresh-scope")
	if imported.StatusCode != 200 || !strings.Contains(string(imported.Body), `"status":"already_present"`) || calls.Load() != 1 {
		t.Fatalf("ABI duplicate result: %s", imported.Body)
	}
	if rejected := call("GET", "codex-sources", "", "old-scope"); rejected.StatusCode != 503 {
		t.Fatal("stale callback scope accepted")
	}
	if missing := call("GET", "webaccounts", "", ""); missing.StatusCode != 503 {
		t.Fatal("missing callback accepted")
	}
	callbackCalls, frees := codexCallbackFixtureCounts()
	if callbackCalls != 5 || frees != callbackCalls {
		t.Fatalf("host callback/free contract: calls=%d frees=%d", callbackCalls, frees)
	}
}
