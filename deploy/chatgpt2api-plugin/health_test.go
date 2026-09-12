package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func healthFixture(t *testing.T, handler http.HandlerFunc) *service {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	plugin := configured(t)
	transport, ok := plugin.client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected private transport")
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "chatgpt2api:80" {
			t.Errorf("unexpected destination: %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	return plugin
}

func statusResponse(t *testing.T, plugin *service) httpResponse {
	t.Helper()
	response := invoke(t, plugin, "management.handle", []byte(`{"Method":"GET","Path":"/v0/management/plugins/chatgpt2api/status","Headers":{"Authorization":["Bearer test-sentinel"],"Cookie":["test-sentinel"]},"Body":"test-sentinel","Query":{"url":["http://invalid/"],"key":["test-sentinel"]}}`))
	if !response.OK {
		t.Fatalf("status RPC failed: %+v", response.Error)
	}
	var result httpResponse
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestHealth_whenHealthy_projectsOnlySafeFields(t *testing.T) {
	plugin := healthFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != "GET" || request.URL.RequestURI() != "/health?format=json" || request.Host != "chatgpt2api:80" {
			t.Error("wrong health request")
		}
		for _, header := range []string{"Authorization", "Cookie", "X-Api-Key"} {
			if request.Header.Get(header) != "" {
				t.Errorf("forwarded %s", header)
			}
		}
		if request.ContentLength > 0 {
			t.Error("forwarded body")
		}
		writer.Header().Set("Set-Cookie", "test-sentinel")
		if _, err := io.WriteString(writer, `{"healthy":true,"version":"0.9.2","accounts":{"active":2,"total":4,"limited":1,"abnormal":1,"labels":["test-sentinel"],"total_quota":999},"storage":{"secret":"test-sentinel"},"proxy_runtime":{"password":"test-sentinel"}}`); err != nil {
			t.Error(err)
		}
	})

	response := statusResponse(t, plugin)

	var result statusView
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || response.Headers.Get("Content-Type") != "application/json" || !result.Upstream.Healthy || result.Upstream.Version != "0.9.2" || result.Upstream.Accounts == nil || *result.Upstream.Accounts.Active != 2 || *result.Upstream.Accounts.Total != 4 || *result.Upstream.Accounts.Limited != 1 || *result.Upstream.Accounts.Abnormal != 1 {
		t.Fatalf("wrong projected response: %s", response.Body)
	}
	if result.Plugin.ID != "chatgpt2api" || result.Plugin.Version != version || result.Plugin.RouteCount != 0 || result.Routing.Provider != "chatgpt2api" || result.Routing.Mode != "native-provider" || result.Routing.CredentialSource != "host-auth-manager" {
		t.Fatalf("wrong integration status: %s", response.Body)
	}
	for _, forbidden := range []string{"test-sentinel", "storage", "total_quota", "proxy_runtime", "labels"} {
		if strings.Contains(string(response.Body), forbidden) {
			t.Fatalf("leaked %s", forbidden)
		}
	}
	if response.Headers.Get("Set-Cookie") != "" {
		t.Fatal("forwarded upstream cookies")
	}
}

func TestHealth_whenInvalid_reportsFixedSafeCode(t *testing.T) {
	for _, scenario := range []struct {
		name, body, code string
		status           int
	}{
		{"http-down", `test-sentinel`, "health_http_error", 503},
		{"malformed", `{"healthy":test-sentinel}`, "health_invalid_response", 200},
		{"missing", `{}`, "health_invalid_response", 200},
		{"null", `null`, "health_invalid_response", 200},
		{"wrong-type", `{"healthy":true,"accounts":{"active":"test-sentinel"}}`, "health_invalid_response", 200},
		{"negative", `{"healthy":true,"accounts":{"active":-1}}`, "health_invalid_response", 200},
		{"oversized", `{"healthy":true,"ignored":"` + strings.Repeat("x", 1024*1024) + `"}`, "health_response_too_large", 200},
		{"trailing", `{"healthy":true} {"secret":"test-sentinel"}`, "health_invalid_response", 200},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			plugin := healthFixture(t, func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(scenario.status)
				if _, err := io.WriteString(writer, scenario.body); err != nil {
					t.Log("fixture body write closed")
				}
			})

			response := statusResponse(t, plugin)

			var result statusView
			if err := json.Unmarshal(response.Body, &result); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 200 || result.Upstream.Healthy || result.Upstream.Error != scenario.code || result.Upstream.Accounts != nil || result.Upstream.Version != "" || strings.Contains(string(response.Body), "test-sentinel") {
				t.Fatalf("unsafe failure: %s", response.Body)
			}
		})
	}
}

func TestHealth_whenDegraded_preservesMeasuredFalseAndOptionalCounts(t *testing.T) {
	plugin := healthFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if _, err := io.WriteString(writer, `{"healthy":false,"accounts":{"active":0}}`); err != nil {
			t.Error(err)
		}
	})

	response := statusResponse(t, plugin)

	var result statusView
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatal(err)
	}
	if result.Upstream.Healthy || result.Upstream.Error != "" || result.Upstream.Accounts == nil || *result.Upstream.Accounts.Active != 0 || result.Upstream.Accounts.Total != nil {
		t.Fatalf("wrong degraded response: %s", response.Body)
	}
}

func TestHealth_whenRedirect_refusesSecondRequest(t *testing.T) {
	count := 0
	plugin := healthFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		count++
		writer.Header().Set("Location", "http://chatgpt2api:80/private?secret=test-sentinel")
		writer.WriteHeader(302)
	})

	response := statusResponse(t, plugin)

	if count != 1 || !strings.Contains(string(response.Body), `"error":"health_http_error"`) {
		t.Fatalf("redirect followed: %d, %s", count, response.Body)
	}
}

func TestHealth_whenConnectionFails_hidesNetworkError(t *testing.T) {
	plugin := configured(t)
	transport := plugin.client.Transport.(*http.Transport)
	transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.AddrError{Err: "test-sentinel", Addr: "test-sentinel"}
	}

	response := statusResponse(t, plugin)

	if strings.Contains(string(response.Body), "test-sentinel") || !strings.Contains(string(response.Body), `"error":"health_unavailable"`) {
		t.Fatalf("unsafe network error: %s", response.Body)
	}
}

func TestHealthClient_whenCreated_hasNoProxyOrPostconnectDeadline(t *testing.T) {
	client := newHealthClient()

	transport, ok := client.Transport.(*http.Transport)

	if !ok || transport.Proxy != nil || client.Timeout != 0 || transport.ResponseHeaderTimeout != 0 || transport.TLSHandshakeTimeout != 0 || transport.IdleConnTimeout != 0 || client.Jar != nil {
		t.Fatal("unsafe transport defaults")
	}
}
