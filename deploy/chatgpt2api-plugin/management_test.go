package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestManagementRegistration_whenCalled_separatesProtectedStatusFromStaticResource(t *testing.T) {
	plugin := configured(t)

	response := invoke(t, plugin, "management.register", []byte(`{}`))

	var result struct {
		Routes    []struct{ Method, Path string }
		Resources []struct{ Path, Menu string }
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if !response.OK || len(result.Routes) != 1 || result.Routes[0].Method != "GET" || result.Routes[0].Path != "/plugins/chatgpt2api/status" || len(result.Resources) != 1 || result.Resources[0].Path != "/index" || result.Resources[0].Menu != "ChatGPT2API" {
		t.Fatalf("wrong routes: %+v", result)
	}
}

func TestResource_whenQueryAndBodyInjected_readsOnlyTrustedStaticHTML(t *testing.T) {
	plugin := configured(t)
	path := filepath.Join(t.TempDir(), "index.html")
	content := []byte("<!doctype html><title>static fixture</title>")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	response := invoke(t, plugin, "plugin.reconfigure", registrationInput(t, "model_names: []\ndashboard_path: "+path))
	if !response.OK {
		t.Fatal(response.Error)
	}

	response = invoke(t, plugin, "management.handle", []byte(`{"Method":"GET","Path":"/v0/resource/plugins/chatgpt2api/index","Query":{"path":["/etc/passwd"],"action":["status"]},"Body":{"secret":"test-sentinel"},"Headers":{"Authorization":["Bearer test-sentinel"]}}`))

	var result httpResponse
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if !response.OK || result.StatusCode != 200 || string(result.Body) != string(content) || result.Headers.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("resource mismatch: %+v", result)
	}
}

func TestManagement_whenWrongMethodOrPath_returnsSafeHTTPError(t *testing.T) {
	for _, scenario := range []struct {
		raw    string
		status int
	}{
		{`{"Method":"POST","Path":"/v0/management/plugins/chatgpt2api/status"}`, 405},
		{`{"Method":"POST","Path":"/v0/resource/plugins/chatgpt2api/index"}`, 405},
		{`{"Method":"GET","Path":"/v0/resource/plugins/chatgpt2api/status"}`, 404},
		{`{"Method":"GET","Path":"/v0/resource/plugins/chatgpt2api/index/../../passwd"}`, 404},
		{`{"Method":"GET","Path":"/v0/management/plugins/chatgpt2api/accounts"}`, 404},
		{`{}`, 400},
		{`null`, 400},
		{`{"Method":4}`, 400},
	} {
		t.Run(scenario.raw, func(t *testing.T) {
			plugin := configured(t)

			response := invoke(t, plugin, "management.handle", []byte(scenario.raw))

			var result httpResponse
			if err := json.Unmarshal(response.Result, &result); err != nil {
				t.Fatal(err)
			}
			if !response.OK || result.StatusCode != scenario.status {
				t.Fatalf("wrong failure: %+v", result)
			}
		})
	}
}

func TestResource_whenMissing_returnsSafeUnavailable(t *testing.T) {
	plugin := configured(t)
	invoke(t, plugin, "plugin.reconfigure", registrationInput(t, "model_names: []\ndashboard_path: "+filepath.Join(t.TempDir(), "missing.html")))

	response := invoke(t, plugin, "management.handle", []byte(`{"Method":"GET","Path":"/v0/resource/plugins/chatgpt2api/index"}`))

	var result httpResponse
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != 503 || string(result.Body) != `{"error":"dashboard_unavailable"}` {
		t.Fatalf("unsafe resource error: %+v", result)
	}
}
