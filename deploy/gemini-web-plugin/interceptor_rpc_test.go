package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestRegistrationEnablesRequestInterceptor_whenPluginLoads(t *testing.T) {
	service := newService(nil)

	result := invoke(t, service, "plugin.register", struct{}{})

	if !result.OK {
		t.Fatalf("registration failed: %+v", result.Error)
	}
	var registration struct {
		Capabilities struct {
			RequestInterceptor bool `json:"request_interceptor"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(result.Result, &registration); err != nil {
		t.Fatal(err)
	}
	if !registration.Capabilities.RequestInterceptor {
		t.Fatal("host will not invoke the request interceptor")
	}
}

func TestAfterInterceptorGuardsOmni_whenAuthSelectsActualModel(t *testing.T) {
	for _, scenario := range []struct {
		name, source, body string
		stream, terminate  bool
	}{
		{"native", "gemini", `{"contents":[{"parts":[{"text":"test"}]}]}`, false, false},
		{"original protocol", "openai", `{"contents":[{"parts":[{"text":"test"}]}]}`, false, true},
		{"stream", "gemini", `{"contents":[{"parts":[{"text":"test"}]}]}`, true, true},
		{"invalid body", "gemini", `{"contents":[],"private":"must-not-reflect"}`, false, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service := newService(func(string, []byte) ([]byte, error) {
				t.Fatal("interceptor invoked host callback")
				return nil, nil
			})
			store := &memorySecrets{}
			service.secrets = store
			request := struct {
				SourceFormat, ToFormat, RequestedModel, Model string
				Stream                                        bool
				Body                                          []byte
			}{scenario.source, "gemini", "selected-alias", omniModel + "(high)", scenario.stream, []byte(scenario.body)}

			result := invoke(t, service, "request.intercept_after", request)

			if !result.OK {
				t.Fatalf("host ignores interceptor RPC errors: %+v", result.Error)
			}
			var response struct {
				Terminate       bool
				StatusCode      int
				ResponseHeaders http.Header
				ResponseBody    []byte
			}
			if err := json.Unmarshal(result.Result, &response); err != nil {
				t.Fatal(err)
			}
			if response.Terminate != scenario.terminate {
				t.Fatalf("wrong Omni decision: %s", result.Result)
			}
			if scenario.terminate && (response.StatusCode != 400 || !json.Valid(response.ResponseBody) || response.ResponseHeaders.Get("Content-Type") != "application/json") {
				t.Fatalf("missing safe termination: %s", result.Result)
			}
			if len(store.reads) != 0 || store.writes != 0 {
				t.Fatal("interceptor accessed secret store")
			}
		})
	}
}
