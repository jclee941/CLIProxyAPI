package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCodexBoundary_whenManagementRegistered_exposesExactProtectedRoutes(t *testing.T) {
	plugin := configured(t)

	response := invoke(t, plugin, "management.register", []byte(`{}`))

	type route struct{ Method, Path string }
	var result struct{ Routes []route }
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	want := []route{
		{"GET", "/plugins/chatgpt2api/status"},
		{"GET", "/plugins/chatgpt2api/codex-sources"},
		{"GET", "/plugins/chatgpt2api/webaccounts"},
		{"POST", "/plugins/chatgpt2api/import-codex"},
		{"POST", "/plugins/chatgpt2api/refresh-web"},
		{"POST", "/plugins/chatgpt2api/set-web-enabled"},
	}
	if !reflect.DeepEqual(result.Routes, want) {
		t.Fatalf("wrong routes: %+v", result.Routes)
	}
}

func TestCodexBoundary_whenCallbackMissing_rejectsAccountOperations(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{"GET", "codex-sources"}, {"GET", "webaccounts"},
		{"POST", "import-codex"}, {"POST", "refresh-web"},
		{"POST", "set-web-enabled"},
	} {
		t.Run(route.path, func(t *testing.T) {
			plugin := configured(t)
			raw, err := json.Marshal(struct{ Method, Path string }{route.method, "/v0/management/plugins/chatgpt2api/" + route.path})
			if err != nil {
				t.Fatal(err)
			}

			response := invoke(t, plugin, "management.handle", raw)

			var result httpResponse
			if err := json.Unmarshal(response.Result, &result); err != nil {
				t.Fatal(err)
			}
			if result.StatusCode != 503 || string(result.Body) != `{"error":"host_callback_required"}` {
				t.Fatalf("missing callback boundary: status=%d body=%s", result.StatusCode, result.Body)
			}
		})
	}
}

func TestCodexBoundary_whenConfigUnknown_rejectsButAcceptsHostReservedFields(t *testing.T) {
	for _, scenario := range []struct {
		config   string
		accepted bool
	}{
		{"model_names: []\nenabled: true\npriority: 12", true},
		{"model_names: []\napi_base_url: http://127.0.0.1:12345", true},
		{"model_names: []\napi_base_url: https://private.example", true},
		{"model_names: []\napi_base_url: http://attacker.example", false},
		{"model_names: []\napi_base_url: https://user:pass@private.example", false},
		{"model_names: []\napi_base_url: https://private.example/path?key=secret", false},
		{"model_names: []\nauth_key: credential-marker", false},
		{"model_names: []\nunknown_provider_option: true", false},
	} {
		t.Run(scenario.config, func(t *testing.T) {
			plugin := configured(t)

			response := invoke(t, plugin, "plugin.reconfigure", registrationInput(t, scenario.config))

			if response.OK != scenario.accepted {
				t.Fatalf("accepted=%v want=%v", response.OK, scenario.accepted)
			}
		})
	}
}
