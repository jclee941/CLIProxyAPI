package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func encodedToken(cookie string) string {
	raw, _ := json.Marshal(struct {
		Cookie   string `json:"cookie"`
		AuthUser int    `json:"auth_user"`
	}{cookie, 2})
	return "gemini-web:v1:" + base64.RawURLEncoding.EncodeToString(raw)
}

func invoke(t *testing.T, service *service, method string, request interface{}) envelope {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := service.handle(context.Background(), method, raw)
	var result envelope
	if err := json.Unmarshal(response, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestAuthParseReturnsOnlyReference_whenStorageIsValid(t *testing.T) {
	service := newService(nil)
	storage := []byte(`{"type":"gemini-web","id":"gemini-web-first.json","label":"First","token_ref":"op://homelab/aaaaaaaaaaaaaaaaaaaaaaaaaa/web-session"}`)
	result := invoke(t, service, "auth.parse", struct{ RawJSON []byte }{storage})
	if !result.OK {
		t.Fatalf("parse failed: %+v", result.Error)
	}
	var response struct {
		Handled bool
		Auth    authData
	}
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	if !response.Handled || response.Auth.Provider != provider || response.Auth.ProxyURL != "direct" {
		t.Fatalf("unexpected parsed auth: %+v", response.Auth)
	}
	if strings.Contains(string(response.Auth.StorageJSON), "cookie") || !strings.Contains(string(response.Auth.StorageJSON), "token_ref") {
		t.Fatal("storage is not reference-only")
	}
}

func TestAuthParseRejectsSecretStorage_whenRawTokenPresent(t *testing.T) {
	service := newService(nil)
	result := invoke(t, service, "auth.parse", struct{ RawJSON []byte }{[]byte(`{"type":"gemini-web","id":"gemini-web-first.json","label":"First","token_ref":"op://homelab/aaaaaaaaaaaaaaaaaaaaaaaaaa/web-session","token":"secret"}`)})
	if result.OK {
		t.Fatal("accepted raw secret storage")
	}
}

func TestCredentialRejectsInvalidSchema_whenUntrusted(t *testing.T) {
	for _, raw := range []string{`{"cookie":"a","auth_user":true}`, `{"cookie":"a","auth_user":-1}`, `{"cookie":"a","auth_user":0,"extra":1}`, `{"cookie":"a","cookie":"b","auth_user":0}`, `{"cookie":"a\r\nb","auth_user":0}`, `{"cookie":"a"}`} {
		t.Run(raw, func(t *testing.T) {
			_, err := parseToken("gemini-web:v1:" + base64.RawURLEncoding.EncodeToString([]byte(raw)))
			if err == nil {
				t.Fatal("accepted invalid token")
			}
		})
	}
}

func TestOmniRejectsUnsupportedCalls_beforeSecretOrNetwork(t *testing.T) {
	for _, scenario := range []struct {
		method, format, payload string
		stream                  bool
	}{
		{"executor.execute", "chat-completions", `{"contents":[{"parts":[{"text":"video"}]}]}`, false},
		{"executor.execute_stream", "gemini", `{"contents":[{"parts":[{"text":"video"}]}]}`, true},
		{"executor.count_tokens", "gemini", `{"contents":[{"parts":[{"text":"video"}]}]}`, false},
		{"executor.execute", "gemini", `{"contents":[{"parts":[{"inlineData":{"data":"AA=="}}]}]}`, false},
		{"executor.execute", "gemini", `{"contents":[{"parts":[{"text":"video"}]}],"tools":[{}]}`, false},
	} {
		t.Run(scenario.method+scenario.format+scenario.payload, func(t *testing.T) {
			service := newService(nil)
			result := invoke(t, service, scenario.method, executorRequest{Model: omniModel, Format: "gemini", SourceFormat: scenario.format, Stream: scenario.stream, Payload: []byte(scenario.payload)})
			if result.OK || result.Error.HTTPStatus != 400 {
				t.Fatalf("expected preflight rejection: %+v", result.Error)
			}
		})
	}
}

func TestExecutorHTTPRejectsUntrustedRoutes_beforeSecrets(t *testing.T) {
	for _, target := range []string{"https://google.com/", "http://gemini-web2api:8081@evil.test/v1/usage", "http://gemini-web2api:8081/v1/usage?key=secret", "http://gemini-web2api:8081/v1beta/models/gemini-web-omni:generateContent", "http://gemini-web2api:8081/other"} {
		t.Run(target, func(t *testing.T) {
			service := newService(nil)
			result := invoke(t, service, "executor.http_request", executorHTTPRequest{Method: "GET", URL: target})
			if result.OK || result.Error.HTTPStatus != 400 {
				t.Fatal("arbitrary HTTP route accepted")
			}
		})
	}
}

func TestResourceIsStaticOnly_whenQueryRequestsSecrets(t *testing.T) {
	service := newService(func(string, []byte) ([]byte, error) { t.Fatal("resource invoked host callback"); return nil, nil })
	service.config.DashboardPath = t.TempDir() + "/missing.html"
	result := invoke(t, service, "management.handle", managementRequest{Method: "GET", Path: resourcePath, Headers: http.Header{"Authorization": {"test-secret"}}, Body: []byte(`{"op":"get","token_ref":"op://homelab/x/web-session"}`)})
	var response httpResponse
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 503 || strings.Contains(string(response.Body), "test-secret") || strings.Contains(string(response.Body), "token_ref") {
		t.Fatalf("unsafe resource response: %+v", response)
	}
}

func TestOmniRejectsNullFields_beforeUpstream(t *testing.T) {
	for _, payload := range []string{
		`{"contents":[{"parts":[{"text":"test"}]}],"generationConfig":null}`,
		`{"contents":[{"parts":[{"text":"test"}]}],"model":null}`,
		`{"contents":[{"role":null,"parts":[{"text":"test"}]}]}`,
	} {
		t.Run(payload, func(t *testing.T) {
			if validateOmni([]byte(payload)) == nil {
				t.Fatal("null field bypassed preflight")
			}
		})
	}
}

func TestAuthParserPreservesHostDisabledState_andAcceptsHostStopMetadata(t *testing.T) {
	service := newService(nil)
	record := recordFixture(t, "a")
	raw := jsonFixture(t, record)
	var stored map[string]json.RawMessage
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	stored["disabled"] = json.RawMessage(`true`)
	stored["request_scoped_errors"] = json.RawMessage(`[{"status":502,"match":["gemini_web_omni:"],"action":"stop"}]`)
	result := invoke(t, service, "auth.parse", struct{ RawJSON []byte }{jsonFixture(t, stored)})
	if !result.OK {
		t.Fatalf("host-persisted auth rejected: %+v", result.Error)
	}
	var response struct{ Auth struct{ Disabled bool } }
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	if !response.Auth.Disabled {
		t.Fatal("disabled account became enabled")
	}
}
