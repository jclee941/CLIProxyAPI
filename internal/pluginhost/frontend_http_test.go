package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type frontendHTTPDouble struct {
	routes []pluginapi.FrontendHTTPRoute
	handle func(context.Context, pluginapi.FrontendHTTPRequest) (pluginapi.FrontendHTTPResponse, error)
}

func (p *frontendHTTPDouble) RegisterFrontendHTTP(context.Context, pluginapi.FrontendHTTPRegistrationRequest) (pluginapi.FrontendHTTPRegistrationResponse, error) {
	routes := append([]pluginapi.FrontendHTTPRoute(nil), p.routes...)
	for i := range routes {
		routes[i].Handler = p
	}
	return pluginapi.FrontendHTTPRegistrationResponse{Routes: routes}, nil
}

func (p *frontendHTTPDouble) HandleFrontendHTTP(ctx context.Context, req pluginapi.FrontendHTTPRequest) (pluginapi.FrontendHTTPResponse, error) {
	return p.handle(ctx, req)
}

func frontendHTTPRecord(id string, priority int, routes []pluginapi.FrontendHTTPRoute, handle func(context.Context, pluginapi.FrontendHTTPRequest) (pluginapi.FrontendHTTPResponse, error)) capabilityRecord {
	return capabilityRecord{id: id, priority: priority, plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{
		FrontendHTTP: &frontendHTTPDouble{routes: routes, handle: handle},
	}}}
}

func TestFrontendHTTPRoutesRejectUnsafeAndOverlappingPatterns(t *testing.T) {
	routes := []pluginapi.FrontendHTTPRoute{
		{Method: "GET", Path: "/v1beta/files/{id}"},
		{Method: "GET", Path: "/v1beta/files/{id}:download"},
		{Method: "GET", Path: "/v1beta/files"},
		{Method: "POST", Path: "/upload/v1beta/files/{session}"},
		{Method: "GET", Path: "/healthz"},
		{Method: "GET", Path: "/v1beta/models/{id}"},
		{Method: "GET", Path: "/v1beta/interactions/{id}"},
	}
	for _, path := range []string{"/", "relative", "/{id}", "/{id}/files", "/v0/management/native", "/v0/resource/plugins/p/file", "/a/../b", "/a/./b", "/a//b", "/a/b/", "/a/%2e%2e", "/a\\b", "/a/*all", "/a/:id", "/a/{id}/{id}", "/a/{id}tail", "/a/{id}:*", "/a/{id}?x=1", "/a/b\n"} {
		routes = append(routes, pluginapi.FrontendHTTPRoute{Method: "GET", Path: path})
	}
	for _, method := range []string{"", "TRACE", "CONNECT", "G ET", "GET\r\nX: value"} {
		routes = append(routes, pluginapi.FrontendHTTPRoute{Method: method, Path: "/unsafe/method"})
	}
	handle := func(context.Context, pluginapi.FrontendHTTPRequest) (pluginapi.FrontendHTTPResponse, error) {
		return pluginapi.FrontendHTTPResponse{Body: []byte("high")}, nil
	}
	host := newHostWithRecords(
		frontendHTTPRecord("high", 10, routes, handle),
		frontendHTTPRecord("low", 1, []pluginapi.FrontendHTTPRoute{
			{Method: "GET", Path: "/v1beta/files/{other}"},
			{Method: "GET", Path: "/v1beta/files/special"},
			{Method: "GET", Path: "/low/only"},
		}, func(context.Context, pluginapi.FrontendHTTPRequest) (pluginapi.FrontendHTTPResponse, error) {
			return pluginapi.FrontendHTTPResponse{Body: []byte("low")}, nil
		}),
	)
	host.RegisterFrontendHTTPRoutes(context.Background(), map[string]struct{}{
		"GET /healthz": {}, "GET /v1beta/models/*action": {}, "GET /v1beta/interactions/:id": {},
	})
	if got := len(host.frontendHTTPRoutes); got != 5 {
		t.Fatalf("registered %d routes, want only 5 safe non-overlapping routes", got)
	}
	for _, path := range []string{"/v1beta/files/a", "/v1beta/files/a:download", "/v1beta/files/special"} {
		w := httptest.NewRecorder()
		if !host.ServeFrontendHTTP(w, httptest.NewRequest("GET", path, nil), frontendCoreScope("trusted")) || w.Body.String() != "high" {
			t.Fatalf("priority dispatch failed for %s", path)
		}
	}
	for _, path := range []string{"/v1beta/files/a/b", "/v1beta/files/..", "/v1beta/files/a:other", "/v1beta/files/a%2Fb", "/healthz", "/v1beta/models/a", "/v1beta/interactions/a"} {
		if host.HasFrontendHTTPRoute("GET", httptest.NewRequest("GET", path, nil).URL.Path) {
			t.Fatalf("matched unsafe or reserved route %s", path)
		}
	}
}

type frontendRPCClient struct {
	call func(context.Context, string, []byte) ([]byte, error)
}

func (c frontendRPCClient) Call(ctx context.Context, method string, request []byte) ([]byte, error) {
	return c.call(ctx, method, request)
}
func (frontendRPCClient) Shutdown() {}

func TestFrontendHTTPRPCRegistrationAndTransport(t *testing.T) {
	var got pluginapi.FrontendHTTPRequest
	var callbackID string
	host := New()
	client := frontendRPCClient{call: func(_ context.Context, method string, raw []byte) ([]byte, error) {
		switch method {
		case pluginabi.MethodPluginRegister:
			return []byte(`{"ok":true,"result":{"schema_version":1,"metadata":{"Name":"http","Version":"1.0.0","Author":"test","GitHubRepository":"https://example.invalid"},"capabilities":{"frontend_http":true}}}`), nil
		case "frontend_http.register":
			return []byte(`{"ok":true,"result":{"Routes":[{"Method":"POST","Path":"/native/{id}"}]}}`), nil
		case "frontend_http.handle":
			var req struct {
				pluginapi.FrontendHTTPRequest
				CallbackID string `json:"host_callback_id"`
			}
			if err := json.Unmarshal(raw, &req); err != nil {
				return nil, err
			}
			got, callbackID = req.FrontendHTTPRequest, req.CallbackID
			return marshalRPCResult(pluginapi.FrontendHTTPResponse{StatusCode: 202, Headers: http.Header{"X-Multi": {"a", "b"}}, Body: req.Body})
		}
		return nil, errors.New("unexpected RPC method")
	}}
	plugin, err := registerRPCPlugin(context.Background(), host, "native", client, pluginabi.MethodPluginRegister, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plugin.Capabilities.FrontendHTTP == nil || !validPlugin(plugin) || !rpcCapabilitiesFromPlugin(plugin).FrontendHTTP {
		t.Fatal("frontend-only plugin not registered as a valid capability")
	}
	setHostSnapshotForTest(host, true, capabilityRecord{id: "native", plugin: plugin})
	host.RegisterFrontendHTTPRoutes(context.Background(), nil)
	body := []byte{0, 255, '<', '&'}
	req := httptest.NewRequest("POST", "/native/%61?q=one&q=two+words&caller_scope=forged", bytes.NewReader(body))
	req.Header["X-Multi"] = []string{"first", "second"}
	req.Header.Set("Caller-Scope", "forged")
	w := httptest.NewRecorder()
	if !host.ServeFrontendHTTP(w, req, frontendCoreScope("trusted-scope")) || w.Code != 202 || !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatal("native RPC status or raw binary response changed")
	}
	if got.CallerScope != "trusted-scope" || got.Path != req.URL.Path || got.RawPath != req.URL.RawPath || got.RawQuery != req.URL.RawQuery || got.RequestURI != req.RequestURI || got.Host != req.Host || got.Params["id"] != "a" || callbackID == "" {
		t.Fatal("trusted identity, URI, parameter, or callback context missing")
	}
	if !reflect.DeepEqual(got.Headers, req.Header) || !reflect.DeepEqual(got.Query, req.URL.Query()) || !bytes.Equal(got.Body, body) {
		t.Fatal("request headers, query, or body changed across RPC")
	}
	if !reflect.DeepEqual(w.Header().Values("X-Multi"), []string{"a", "b"}) {
		t.Fatal("response headers changed across RPC")
	}
}

type frontendErrorReader struct{}

func (frontendErrorReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

type frontendZeroReader struct{}

func (frontendZeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestFrontendHTTPRejectsMissingScopeAndInvalidBodies(t *testing.T) {
	var calls atomic.Int32
	host := newHostWithRecords(frontendHTTPRecord("native", 0, []pluginapi.FrontendHTTPRoute{{Method: "POST", Path: "/native/body"}},
		func(context.Context, pluginapi.FrontendHTTPRequest) (pluginapi.FrontendHTTPResponse, error) {
			calls.Add(1)
			return pluginapi.FrontendHTTPResponse{}, nil
		}))
	host.RegisterFrontendHTTPRoutes(context.Background(), nil)
	for _, test := range []struct {
		name, scope string
		body        io.Reader
		length      int64
		status      int
	}{
		{name: "missing scope", status: 401},
		{name: "declared oversize", scope: "trusted", body: frontendErrorReader{}, length: pluginapi.FrontendHTTPMaxBodyBytes + 1, status: 413},
		{name: "chunked oversize", scope: "trusted", body: io.LimitReader(frontendZeroReader{}, pluginapi.FrontendHTTPMaxBodyBytes+1), length: -1, status: 413},
		{name: "read failure", scope: "trusted", body: frontendErrorReader{}, length: -1, status: 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/native/body", test.body)
			req.ContentLength = test.length
			w := httptest.NewRecorder()
			if !host.ServeFrontendHTTP(w, req, frontendCoreScope(test.scope)) || w.Code != test.status {
				t.Fatalf("status=%d, want %d", w.Code, test.status)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("invalid request reached plugin")
	}
}

func TestFrontendHTTPFailureDoesNotBecomeSuccess(t *testing.T) {
	for _, test := range []struct {
		name   string
		handle func(context.Context, pluginapi.FrontendHTTPRequest) (pluginapi.FrontendHTTPResponse, error)
	}{
		{"error", func(context.Context, pluginapi.FrontendHTTPRequest) (pluginapi.FrontendHTTPResponse, error) {
			return pluginapi.FrontendHTTPResponse{}, errors.New("private failure")
		}},
		{"panic", func(context.Context, pluginapi.FrontendHTTPRequest) (pluginapi.FrontendHTTPResponse, error) {
			panic("private failure")
		}},
		{"bad status", func(context.Context, pluginapi.FrontendHTTPRequest) (pluginapi.FrontendHTTPResponse, error) {
			return pluginapi.FrontendHTTPResponse{StatusCode: 99}, nil
		}},
		{"bad header", func(context.Context, pluginapi.FrontendHTTPRequest) (pluginapi.FrontendHTTPResponse, error) {
			return pluginapi.FrontendHTTPResponse{Headers: http.Header{"X-Bad": {"a\r\nb"}}}, nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			host := newHostWithRecords(frontendHTTPRecord("native", 0, []pluginapi.FrontendHTTPRoute{{Method: "GET", Path: "/native/fail"}}, test.handle))
			host.RegisterFrontendHTTPRoutes(context.Background(), nil)
			w := httptest.NewRecorder()
			if !host.ServeFrontendHTTP(w, httptest.NewRequest("GET", "/native/fail", nil), frontendCoreScope("trusted")) || w.Code != 502 || strings.Contains(w.Body.String(), "private") {
				t.Fatal("plugin failure became success or disclosed private error")
			}
		})
	}
}

func TestFrontendHTTPReloadAndConcurrentDispatch(t *testing.T) {
	host := newHostWithRecords(frontendHTTPRecord("native", 0, []pluginapi.FrontendHTTPRoute{{Method: "GET", Path: "/native/ok"}},
		func(context.Context, pluginapi.FrontendHTTPRequest) (pluginapi.FrontendHTTPResponse, error) {
			return pluginapi.FrontendHTTPResponse{Body: []byte("ok")}, nil
		}))
	host.RegisterFrontendHTTPRoutes(context.Background(), nil)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 30 {
				w := httptest.NewRecorder()
				if !host.ServeFrontendHTTP(w, httptest.NewRequest("GET", "/native/ok", nil), frontendCoreScope("trusted")) || w.Code != 200 || w.Body.String() != "ok" {
					t.Error("concurrent refresh lost a route")
				}
			}
		})
	}
	for range 30 {
		host.RegisterFrontendHTTPRoutes(context.Background(), nil)
	}
	wg.Wait()
	host.ApplyConfig(context.Background(), &config.Config{})
	if host.HasFrontendHTTPRoute("GET", "/native/ok") {
		t.Fatal("disabled plugin still routes")
	}
}

func TestFrontendHTTPStoppedDuringBodyRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	var calls atomic.Int32
	host := newHostWithRecords(frontendHTTPRecord("native", 0, []pluginapi.FrontendHTTPRoute{{Method: "POST", Path: "/native/body"}},
		func(context.Context, pluginapi.FrontendHTTPRequest) (pluginapi.FrontendHTTPResponse, error) {
			calls.Add(1)
			return pluginapi.FrontendHTTPResponse{}, nil
		}))
	host.RegisterFrontendHTTPRoutes(ctx, nil)
	started := make(chan struct{})
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	req := httptest.NewRequest("POST", "/native/body", &frontendGateReader{ctx: ctx, started: started, release: release})
	w := httptest.NewRecorder()
	done := make(chan bool, 1)
	go func() { done <- host.ServeFrontendHTTP(w, req, frontendCoreScope("trusted")) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("body reader not entered")
	}
	host.ShutdownAll()
	unblock()
	select {
	case handled := <-done:
		if !handled || w.Code != 503 || calls.Load() != 0 {
			t.Fatal("stopped plugin dispatched or returned empty success")
		}
	case <-ctx.Done():
		t.Fatal("request did not finish after shutdown")
	}
}

type frontendGateReader struct {
	ctx     context.Context
	started chan struct{}
	release chan struct{}
}

func (r *frontendGateReader) Read([]byte) (int, error) {
	close(r.started)
	select {
	case <-r.release:
		return 0, io.EOF
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	}
}
