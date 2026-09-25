package main

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
)

func invoke(t *testing.T, plugin *service, method string, raw []byte) envelope {
	t.Helper()
	var response envelope
	if err := json.Unmarshal(plugin.handle(t.Context(), method, raw), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func registrationInput(t *testing.T, config string) []byte {
	t.Helper()
	raw, err := json.Marshal(struct {
		ConfigYAML    []byte `json:"config_yaml"`
		SchemaVersion int    `json:"schema_version"`
	}{[]byte(config), 6})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func configured(t *testing.T) *service {
	t.Helper()
	plugin := newService()
	t.Cleanup(plugin.client.CloseIdleConnections)
	response := invoke(t, plugin, "plugin.register", registrationInput(t, "model_names: [gpt-test, gpt-alias]"))
	if !response.OK {
		t.Fatalf("registration: %+v", response.Error)
	}
	return plugin
}

func TestRegistration_whenExplicitConfig_exposesOnlyDelegationCapabilities(t *testing.T) {
	plugin := newService()

	response := invoke(t, plugin, "plugin.register", registrationInput(t, `{"model_names":[]}`))

	var result struct {
		SchemaVersion int `json:"schema_version"`
		Metadata      struct{ Name, Version string }
		Capabilities  map[string]bool
	}
	if !response.OK {
		t.Fatalf("registration: %+v", response.Error)
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != 6 || result.Metadata.Name != "chatgpt2api" || result.Metadata.Version != version || !reflect.DeepEqual(result.Capabilities, map[string]bool{"model_router": true, "management_api": true}) {
		t.Fatalf("wrong registration: %+v", result)
	}
}

func TestRoute_whenActualCoreProviderIsNamespaced_selectsNativeAuthPath(t *testing.T) {
	plugin := configured(t)
	request := routeRequest{RequestedModel: "gpt-test", AvailableProviders: []string{"openai-compatible-chatgpt2api"}}
	result := plugin.route(request)
	if !result.Handled || result.Target != "openai-compatible-chatgpt2api" || result.TargetKind != "provider" || result.TargetModel != "" {
		t.Fatalf("the actual core provider namespace was not selected: %+v", result)
	}
}

func TestRoute_whenExactModelAndProviderAvailable_delegatesWithoutRewrite(t *testing.T) {
	for _, raw := range []string{
		`{"RequestedModel":"gpt-test","AvailableProviders":["openai-compatible-chatgpt2api"]}`,
		`{"RequestedModel":" gpt-alias ","AvailableProviders":["other"," OPENAI-COMPATIBLE-CHATGPT2API "],"SourceFormat":"unknown","Body":{"not":"a payload to inspect"}}`,
		`{"RequestedModel":"gpt-test","AvailableProviders":["openai-compatible-chatgpt2api"],"SourceFormat":"","Body":"not-base64","Headers":{"Authorization":["Bearer test-sentinel"]}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			plugin := configured(t)

			response := invoke(t, plugin, "model.route", []byte(raw))

			var result routeResponse
			if err := json.Unmarshal(response.Result, &result); err != nil {
				t.Fatal(err)
			}
			if !response.OK || !result.Handled || result.TargetKind != "provider" || result.Target != nativeProvider || result.TargetModel != "" || plugin.routeCount.Load() != 1 {
				t.Fatalf("wrong decision: %+v", result)
			}
		})
	}
}

func TestRoute_whenOutsideScope_declinesUnchanged(t *testing.T) {
	for _, raw := range []string{
		`{}`, `null`, `{"RequestedModel":""}`, `{"RequestedModel":"unknown","AvailableProviders":["openai-compatible-chatgpt2api"]}`,
		`{"RequestedModel":"GPT-test","AvailableProviders":["openai-compatible-chatgpt2api"]}`,
		`{"RequestedModel":"gpt-test(suffix)","AvailableProviders":["openai-compatible-chatgpt2api"]}`,
		`{"RequestedModel":"gpt-test","AvailableProviders":["other"]}`,
		`{"RequestedModel":"gpt-test","AvailableProviders":["chatgpt2api"]}`,
		`{"RequestedModel":"auto","AvailableProviders":["openai-compatible-chatgpt2api"]}`,
		`{"RequestedModel":"gpt-image-2","AvailableProviders":["openai-compatible-chatgpt2api"]}`,
		`{"Body":{"model":"gpt-test"},"AvailableProviders":["openai-compatible-chatgpt2api"]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			plugin := configured(t)

			response := invoke(t, plugin, "model.route", []byte(raw))

			var result routeResponse
			if err := json.Unmarshal(response.Result, &result); err != nil {
				t.Fatal(err)
			}
			if !response.OK || result != (routeResponse{}) || plugin.routeCount.Load() != 0 {
				t.Fatalf("hijacked: %+v", response)
			}
		})
	}
}

func TestConfiguration_whenInvalid_rejectsWithoutReplacingPreviousScope(t *testing.T) {
	for _, config := range []string{"", "{}", "model_names: null", "model_names: [auto]", "model_names: [ Auto ]", "model_names: [gpt-image-2]", "model_names: [universalauto]", "model_names: [gpt-test, ' gpt-test ']", "model_names: ['']", "model_names: [42]", "model_names: []\ndashboard_path: relative.html"} {
		t.Run(config, func(t *testing.T) {
			plugin := configured(t)

			response := invoke(t, plugin, "plugin.reconfigure", registrationInput(t, config))

			if response.OK || response.Error.HTTPStatus != 400 {
				t.Fatalf("accepted bad config: %+v", response)
			}
			if !reflect.DeepEqual([]string(plugin.settings().ModelNames), []string{"gpt-test", "gpt-alias"}) {
				t.Fatal("failed reload replaced config")
			}
		})
	}
}

func TestReconfigure_whenEmpty_declinesAndRetainsAttemptCounter(t *testing.T) {
	plugin := configured(t)
	invoke(t, plugin, "model.route", []byte(`{"RequestedModel":"gpt-test","AvailableProviders":["openai-compatible-chatgpt2api"]}`))

	response := invoke(t, plugin, "plugin.reconfigure", registrationInput(t, "model_names: []"))

	if !response.OK || plugin.routeCount.Load() != 1 || len(plugin.settings().ModelNames) != 0 {
		t.Fatal("reload contract failed")
	}
	decision := invoke(t, plugin, "model.route", []byte(`{"RequestedModel":"gpt-test","AvailableProviders":["openai-compatible-chatgpt2api"]}`))
	var result routeResponse
	if err := json.Unmarshal(decision.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Handled {
		t.Fatal("removed scope still routed")
	}
}

func TestDispatch_whenInvalid_returnsSafeErrors(t *testing.T) {
	for _, method := range []string{"auth.parse", "model.register", "executor.execute", "anything"} {
		response := invoke(t, configured(t), method, []byte(`{"secret":"test-sentinel"}`))
		if response.OK || response.Error.Code != "unknown_method" {
			t.Fatalf("unsupported method: %+v", response)
		}
	}
	response := invoke(t, configured(t), "model.route", []byte(`{"RequestedModel":42}`))
	if response.OK || response.Error.HTTPStatus != 400 {
		t.Fatal("invalid router input accepted")
	}
}

func TestConcurrency_whenRoutingAndReloading_counterIsRaceSafe(t *testing.T) {
	plugin := configured(t)
	input := registrationInput(t, "model_names: [gpt-test]")
	var workers sync.WaitGroup

	for range 20 {
		workers.Go(func() {
			for range 30 {
				invoke(t, plugin, "plugin.reconfigure", input)
				invoke(t, plugin, "model.route", []byte(`{"RequestedModel":"gpt-test","AvailableProviders":["openai-compatible-chatgpt2api"]}`))
				plugin.client.CloseIdleConnections()
			}
		})
	}
	workers.Wait()

	if plugin.routeCount.Load() != 600 {
		t.Fatalf("counter: %d", plugin.routeCount.Load())
	}
}
