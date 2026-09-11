package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func TestOmniInterceptorTerminatesNonGeminiRoutes_whenBodyHasGeminiShape(t *testing.T) {
	for _, source := range []string{"openai", "openai-response", "claude", "gemini-cli", ""} {
		t.Run(source, func(t *testing.T) {
			service := newService(func(string, []byte) ([]byte, error) { t.Fatal("interceptor called host"); return nil, nil })
			store := &memorySecrets{}
			service.secrets = store
			request := struct { SourceFormat, Model string; Body []byte }{source, omniModel, []byte(`{"contents":[{"parts":[{"text":"test"}]}]}`)}

			result := invoke(t, service, "request.intercept_before", request)

			if !result.OK { t.Fatalf("host ignores interceptor errors: %+v", result.Error) }
			var response struct { Terminate bool; StatusCode int; ResponseHeaders http.Header; ResponseBody []byte }
			if err := json.Unmarshal(result.Result, &response); err != nil { t.Fatal(err) }
			if !response.Terminate || response.StatusCode != 400 || !json.Valid(response.ResponseBody) || response.ResponseHeaders.Get("Content-Type") != "application/json" { t.Fatalf("missing safe termination: %s", result.Result) }
			if len(store.reads) != 0 || store.writes != 0 { t.Fatal("preflight resolved a secret") }
		})
	}
}

func TestOmniInterceptorScopesRequestedAndActualModels_withThinkingSuffix(t *testing.T) {
	for _, models := range []struct { requested, actual string; terminate bool }{
		{omniModel, "alias", true}, {"alias", omniModel, true},
		{omniModel+"(high)", "alias", true}, {"alias", omniModel+"(8192)", true},
		{"team/"+omniModel, flashModel, false}, {omniModel+"-other", "other", false},
		{omniModel+"(high", "other", false}, {omniModel+"(high)(low)", "other", false},
	} {
		t.Run(models.requested+models.actual, func(t *testing.T) {
			service := newService(nil)
			request := struct { SourceFormat, RequestedModel, Model string; Body []byte }{"openai", models.requested, models.actual, []byte(`{}`)}

			result := invoke(t, service, "request.intercept_before", request)

			if !result.OK { t.Fatalf("interceptor RPC failed: %+v", result.Error) }
			var response struct { Terminate bool }
			if err := json.Unmarshal(result.Result, &response); err != nil { t.Fatal(err) }
			if response.Terminate != models.terminate { t.Fatalf("wrong model scope: %s", result.Result) }
		})
	}
}

func TestOmniInterceptorReusesPayloadValidation_whenGeminiRoute(t *testing.T) {
	for _, scenario := range []struct { body string; stream, terminate bool }{
		{`{"contents":[{"role":"user","parts":[{"text":"test"}]}]}`, false, false},
		{`{"contents":[{"parts":[{"text":"test"}]}]}`, true, true},
		{`{"contents":[{"parts":[{"inlineData":{"data":"AA=="}}]}]}`, false, true},
		{`{"contents":[{"parts":[{"text":"test"}]}],"tools":[{}]}`, false, true},
		{`{"contents":[{"parts":[{"text":"test"}]}],"generationConfig":null}`, false, true},
		{`{`, false, true},
	} {
		t.Run(scenario.body, func(t *testing.T) {
			request := struct { SourceFormat, RequestedModel, Model string; Stream bool; Body []byte }{"gemini", omniModel+"(high)", omniModel, scenario.stream, []byte(scenario.body)}

			result := invoke(t, newService(nil), "request.intercept_before", request)

			if !result.OK { t.Fatalf("interceptor RPC failed: %+v", result.Error) }
			var response struct { Terminate bool }
			if err := json.Unmarshal(result.Result, &response); err != nil { t.Fatal(err) }
			if response.Terminate != scenario.terminate { t.Fatalf("wrong payload decision: %s", result.Result) }
		})
	}
}

func TestInterceptorPreservesStructuredOutputChanges_whenOrderedBeforeOrAfter(t *testing.T) {
	for _, method := range []string{"request.intercept_before", "request.intercept_after"} {
		for _, model := range []string{flashModel, "gemini-web-flash", "claude-sonnet", "gpt-5", ""} {
			for _, preceding := range []bool{true, false} {
				t.Run(method+model+string(rune('0'+map[bool]int{false:0,true:1}[preceding])), func(t *testing.T) {
					body := []byte(`{"messages":[{"role":"user","content":"test"}],"response_format":{"type":"json_object"}}`)
					headers := http.Header{"X-Structured-Output": {"keep"}, "Authorization": {"test-header"}}
					request := struct { SourceFormat, Model string; Body []byte; Headers http.Header; Metadata json.RawMessage }{"openai", model, body, headers, json.RawMessage(`{}`)}
					if !preceding { request.Body = []byte(`{}`); request.Headers = nil }

					result := invoke(t, newService(nil), method, request)

					if !result.OK { t.Fatalf("interceptor RPC failed: %+v", result.Error) }
					var modifications map[string]json.RawMessage
					if err := json.Unmarshal(result.Result, &modifications); err != nil { t.Fatal(err) }
					if len(modifications) != 0 { t.Fatalf("interceptor rewrote another plugin's request: %s", result.Result) }
					if preceding && (!bytes.Equal(request.Body, body) || request.Headers.Get("X-Structured-Output") != "keep") { t.Fatal("previous interceptor changes lost") }
				})
			}
		}
	}
}
