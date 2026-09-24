package pluginhost

import (
	"bytes"
	"context"
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
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/session"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func frontendCoreScope(scope string) func() (string, bool) {
	return func() (string, bool) { return scope, true }
}

type frontendCountReader struct {
	reads int
	body  io.Reader
}

func (r *frontendCountReader) Read(p []byte) (int, error) {
	r.reads++
	return r.body.Read(p)
}

func TestFrontendHTTPScopedAuthenticationPreservesScopeBeforeBody(t *testing.T) {
	scope := session.CallerScope("original-api-key")
	body := &frontendCountReader{body: bytes.NewReader([]byte{0, 255, 1})}
	var authCalls, coreCalls int
	var received pluginapi.FrontendHTTPRequest
	provider := frontendAuthProviderFunc{authenticate: func(_ context.Context, req pluginapi.FrontendAuthRequest) (pluginapi.FrontendAuthResponse, error) {
		authCalls++
		if body.reads != 0 || req.Body != nil || req.Method != "POST" || req.Path != "/upload/v1beta/files/resumable" || req.Query.Get("upload_id") != "opaque" || req.Headers.Get("X-Test") != "original" {
			t.Errorf("scoped auth read body early or lost request metadata: reads=%d request=%+v", body.reads, req)
		}
		req.Headers.Set("X-Test", "mutated")
		req.Query.Set("upload_id", "mutated")
		return pluginapi.FrontendAuthResponse{Authenticated: true, CallerScope: scope, Principal: "ignored", Metadata: map[string]string{"caller_scope": "ignored"}}, nil
	}}
	record := frontendHTTPRecord("scoped", 1, []pluginapi.FrontendHTTPRoute{
		{Method: "POST", Path: "/upload/v1beta/files/resumable", AuthMode: pluginapi.FrontendHTTPAuthScoped, Authenticator: provider},
		{Method: "POST", Path: "/upload/v1beta/files", Authenticator: provider},
	}, func(_ context.Context, req pluginapi.FrontendHTTPRequest) (pluginapi.FrontendHTTPResponse, error) {
		received = req
		return pluginapi.FrontendHTTPResponse{StatusCode: 201}, nil
	})
	host := newHostWithRecords(record)
	host.RegisterFrontendHTTPRoutes(t.Context(), nil)
	coreAuth := func() (string, bool) { coreCalls++; return scope, true }
	start := httptest.NewRecorder()
	if !host.ServeFrontendHTTP(start, httptest.NewRequest("POST", "/upload/v1beta/files", nil), coreAuth) || start.Code != 201 || coreCalls != 1 || authCalls != 0 || received.CallerScope != scope {
		t.Fatal("default route did not retain core auth")
	}
	req := httptest.NewRequest("POST", "/upload/v1beta/files/resumable?upload_id=opaque&caller_scope=forged", body)
	req.Header.Set("X-Test", "original")
	req.Header.Set("Caller-Scope", "forged")
	w := httptest.NewRecorder()
	if !host.ServeFrontendHTTP(w, req, coreAuth) || w.Code != 201 || coreCalls != 1 || authCalls != 1 {
		t.Fatalf("wrong auth dispatch: status=%d core=%d scoped=%d", w.Code, coreCalls, authCalls)
	}
	if received.CallerScope != scope || !bytes.Equal(received.Body, []byte{0, 255, 1}) || received.Headers.Get("X-Test") != "original" || received.Query.Get("upload_id") != "opaque" {
		t.Fatalf("scope rehashed/forged or request mutated: %+v", received)
	}
	if host.ServeFrontendHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/missing", nil), coreAuth) || coreCalls != 1 {
		t.Fatal("unmatched route invoked core auth")
	}
}

func TestFrontendHTTPScopedAuthenticationRejectsWithoutReadingOrFallback(t *testing.T) {
	scope := session.CallerScope("original")
	for _, test := range []struct {
		name     string
		response pluginapi.FrontendAuthResponse
		err      error
		panics   bool
		status   int
	}{
		{name: "declined", response: pluginapi.FrontendAuthResponse{CallerScope: scope}},
		{name: "missing scope", response: pluginapi.FrontendAuthResponse{Authenticated: true, Principal: "principal", Metadata: map[string]string{"caller_scope": scope}}},
		{name: "short scope", response: pluginapi.FrontendAuthResponse{Authenticated: true, CallerScope: scope[:63]}},
		{name: "long scope", response: pluginapi.FrontendAuthResponse{Authenticated: true, CallerScope: scope + "0"}},
		{name: "uppercase scope", response: pluginapi.FrontendAuthResponse{Authenticated: true, CallerScope: strings.ToUpper(scope)}},
		{name: "nonhex scope", response: pluginapi.FrontendAuthResponse{Authenticated: true, CallerScope: strings.Repeat("z", 64)}},
		{name: "spaced scope", response: pluginapi.FrontendAuthResponse{Authenticated: true, CallerScope: " " + scope}},
		{name: "error with success", response: pluginapi.FrontendAuthResponse{Authenticated: true, CallerScope: scope}, err: errors.New("private bearer secret")},
		{name: "panic", panics: true},
		{name: "rejection", response: pluginapi.FrontendAuthResponse{Rejection: &pluginapi.FrontendHTTPResponse{StatusCode: 410, Headers: http.Header{"X-Rejected": {"first", "second"}}, Body: []byte{0, 255}}}, status: 410},
		{name: "success with rejection", response: pluginapi.FrontendAuthResponse{Authenticated: true, CallerScope: scope, Rejection: &pluginapi.FrontendHTTPResponse{StatusCode: 409}}, status: 409},
		{name: "bad scope with rejection", response: pluginapi.FrontendAuthResponse{Authenticated: true, CallerScope: "bad", Rejection: &pluginapi.FrontendHTTPResponse{StatusCode: 403}}, status: 403},
		{name: "server rejection", response: pluginapi.FrontendAuthResponse{Rejection: &pluginapi.FrontendHTTPResponse{StatusCode: 599}}, status: 599},
		{name: "zero status", response: pluginapi.FrontendAuthResponse{Rejection: &pluginapi.FrontendHTTPResponse{}}},
		{name: "success rejection", response: pluginapi.FrontendAuthResponse{Rejection: &pluginapi.FrontendHTTPResponse{StatusCode: 200}}},
		{name: "redirect rejection", response: pluginapi.FrontendAuthResponse{Rejection: &pluginapi.FrontendHTTPResponse{StatusCode: 302}}},
		{name: "invalid rejection status", response: pluginapi.FrontendAuthResponse{Rejection: &pluginapi.FrontendHTTPResponse{StatusCode: 600}}},
		{name: "invalid rejection header", response: pluginapi.FrontendAuthResponse{Rejection: &pluginapi.FrontendHTTPResponse{StatusCode: 403, Headers: http.Header{"X-Bad": {"private\r\nsecret"}}}}},
		{name: "invalid rejection header name", response: pluginapi.FrontendAuthResponse{Rejection: &pluginapi.FrontendHTTPResponse{StatusCode: 403, Headers: http.Header{"bad header": {"private"}}}}},
		{name: "error with rejection", response: pluginapi.FrontendAuthResponse{Rejection: &pluginapi.FrontendHTTPResponse{StatusCode: 410}}, err: errors.New("private")},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &frontendCountReader{body: strings.NewReader("unread")}
			provider := frontendAuthProviderFunc{authenticate: func(_ context.Context, req pluginapi.FrontendAuthRequest) (pluginapi.FrontendAuthResponse, error) {
				if req.Body != nil || body.reads != 0 {
					t.Fatal("auth received or read request body")
				}
				if test.panics {
					panic("private bearer secret")
				}
				return test.response, test.err
			}}
			host := newHostWithRecords(frontendHTTPRecord("scoped", 1, []pluginapi.FrontendHTTPRoute{{Method: "POST", Path: "/upload/resumable", AuthMode: pluginapi.FrontendHTTPAuthScoped, Authenticator: provider}},
				func(context.Context, pluginapi.FrontendHTTPRequest) (pluginapi.FrontendHTTPResponse, error) {
					t.Fatal("rejected request reached handler")
					return pluginapi.FrontendHTTPResponse{}, nil
				}))
			host.RegisterFrontendHTTPRoutes(t.Context(), nil)
			req := httptest.NewRequest("POST", "/upload/resumable?upload_id=tampered", body)
			req.ContentLength = pluginapi.FrontendHTTPMaxBodyBytes + 1
			w := httptest.NewRecorder()
			handled := host.ServeFrontendHTTP(w, req, func() (string, bool) {
				t.Fatal("failed scoped authentication fell back to core auth")
				return scope, true
			})
			status := test.status
			if status == 0 {
				status = http.StatusUnauthorized
			}
			if !handled || w.Code != status || body.reads != 0 || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("unsafe auth rejection: handled=%t status=%d want=%d reads=%d body=%q", handled, w.Code, status, body.reads, w.Body.String())
			}
			if test.name == "rejection" && (!bytes.Equal(w.Body.Bytes(), []byte{0, 255}) || !reflect.DeepEqual(w.Header().Values("X-Rejected"), []string{"first", "second"})) {
				t.Fatal("rejection body or repeated headers changed")
			}
			if test.panics && !host.isPluginFused("scoped") {
				t.Fatal("scoped authenticator panic did not fuse plugin")
			}
		})
	}
}

func TestFrontendHTTPRejectsUnsafeAuthenticationModes(t *testing.T) {
	provider := frontendAuthProviderFunc{}
	host := newHostWithRecords(frontendHTTPRecord("auth-modes", 1, []pluginapi.FrontendHTTPRoute{
		{Method: "GET", Path: "/auth/default"},
		{Method: "GET", Path: "/auth/scoped", AuthMode: pluginapi.FrontendHTTPAuthScoped, Authenticator: provider},
		{Method: "GET", Path: "/auth/missing", AuthMode: pluginapi.FrontendHTTPAuthScoped},
		{Method: "GET", Path: "/auth/unknown", AuthMode: "anonymous", Authenticator: provider},
		{Method: "GET", Path: "/auth/spaced", AuthMode: " scoped", Authenticator: provider},
	}, nil))
	host.RegisterFrontendHTTPRoutes(t.Context(), nil)
	if len(host.frontendHTTPRoutes) != 2 || !host.HasFrontendHTTPRoute("GET", "/auth/default") || !host.HasFrontendHTTPRoute("GET", "/auth/scoped") {
		t.Fatalf("invalid authentication configuration published: %+v", host.frontendHTTPRoutes)
	}
}

func TestFrontendHTTPCoreAuthenticationRejectionIsPreserved(t *testing.T) {
	host := newHostWithRecords(frontendHTTPRecord("default", 1, []pluginapi.FrontendHTTPRoute{{Method: "POST", Path: "/auth/default"}}, nil))
	host.RegisterFrontendHTTPRoutes(t.Context(), nil)
	for _, configured := range []bool{false, true} {
		body := &frontendCountReader{body: strings.NewReader("unread")}
		w := httptest.NewRecorder()
		var coreAuth func() (string, bool)
		want := 401
		if configured {
			want = 403
			coreAuth = func() (string, bool) { w.WriteHeader(403); return "", false }
		}
		if !host.ServeFrontendHTTP(w, httptest.NewRequest("POST", "/auth/default", body), coreAuth) || w.Code != want || body.reads != 0 {
			t.Fatalf("core rejection overwritten or body read: status=%d reads=%d", w.Code, body.reads)
		}
	}
}

func TestFrontendHTTPAuthenticationPinsOwnerAndGeneration(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		for _, change := range []string{"reload", "owner replacement", "shutdown"} {
			t.Run(fmtAuthMode(scoped)+"/"+change, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				started, release := make(chan struct{}), make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				defer unblock()
				scope := session.CallerScope("original")
				authenticate := func() (string, bool) {
					close(started)
					select {
					case <-release:
					case <-ctx.Done():
						t.Error("authentication release timed out")
					}
					return scope, true
				}
				route := pluginapi.FrontendHTTPRoute{Method: "POST", Path: "/auth/pinned"}
				if scoped {
					route.AuthMode = pluginapi.FrontendHTTPAuthScoped
					route.Authenticator = frontendAuthProviderFunc{authenticate: func(context.Context, pluginapi.FrontendAuthRequest) (pluginapi.FrontendAuthResponse, error) {
						scope, _ := authenticate()
						return pluginapi.FrontendAuthResponse{Authenticated: true, CallerScope: scope}, nil
					}}
				}
				var calls atomic.Int32
				record := frontendHTTPRecord("original", 1, []pluginapi.FrontendHTTPRoute{route}, func(context.Context, pluginapi.FrontendHTTPRequest) (pluginapi.FrontendHTTPResponse, error) {
					calls.Add(1)
					return pluginapi.FrontendHTTPResponse{}, nil
				})
				host := newHostWithRecords(record)
				host.RegisterFrontendHTTPRoutes(ctx, nil)
				body := &frontendCountReader{body: strings.NewReader("unread")}
				w, done := httptest.NewRecorder(), make(chan bool, 1)
				go func() {
					done <- host.ServeFrontendHTTP(w, httptest.NewRequest("POST", "/auth/pinned", body), authenticate)
				}()
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatal("authentication not entered")
				}
				switch change {
				case "reload":
					host.ApplyConfig(ctx, &config.Config{})
					setHostSnapshotForTest(host, true, record)
					host.RegisterFrontendHTTPRoutes(ctx, nil)
				case "owner replacement":
					record.id = "replacement"
					setHostSnapshotForTest(host, true, record)
					host.RegisterFrontendHTTPRoutes(ctx, nil)
				case "shutdown":
					host.ShutdownAll()
				}
				unblock()
				select {
				case handled := <-done:
					if !handled || w.Code != 503 || body.reads != 0 || calls.Load() != 0 {
						t.Fatalf("stale authentication dispatched: handled=%t status=%d reads=%d calls=%d", handled, w.Code, body.reads, calls.Load())
					}
				case <-ctx.Done():
					t.Fatal("request did not finish after authentication")
				}
			})
		}
	}
}

func fmtAuthMode(scoped bool) string {
	if scoped {
		return "scoped"
	}
	return "core"
}

func TestFrontendHTTPGlobalAuthIgnoresRouteLocalFields(t *testing.T) {
	for _, scope := range []string{"invalid", session.CallerScope("other-key")} {
		host := New()
		adapter := newAccessAdapterForTest(host, "global", frontendAuthProviderFunc{identifier: "global", authenticate: func(context.Context, pluginapi.FrontendAuthRequest) (pluginapi.FrontendAuthResponse, error) {
			return pluginapi.FrontendAuthResponse{Authenticated: true, Principal: "core-key", Metadata: map[string]string{"normal": "value"}, CallerScope: scope, Rejection: &pluginapi.FrontendHTTPResponse{StatusCode: 403}}, nil
		}})
		result, err := adapter.Authenticate(t.Context(), httptest.NewRequest("GET", "/v1/models", nil))
		if err != nil || result == nil || result.Principal != "core-key" || !reflect.DeepEqual(result.Metadata, map[string]string{"normal": "value"}) {
			t.Fatalf("route-local fields changed global authentication: result=%+v err=%v", result, err)
		}
	}
}
