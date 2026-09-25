//go:build cgo && (linux || darwin || freebsd)

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pluginhost"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v7/sdk/access"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/session"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func TestFrontendHTTPNativeRouting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, runtime.GOOS, runtime.GOARCH)
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ext := ".so"
	if runtime.GOOS == "darwin" {
		ext = ".dylib"
	}
	command := exec.CommandContext(ctx, "cc", "-shared", "-fPIC", "-pthread", "-Wall", "-Wextra", "-Werror", "-o", filepath.Join(pluginDir, "frontend"+ext), "testdata/frontend_http.c")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compile native fixture: %v\n%s", err, output)
	}
	host := pluginhost.New()
	t.Cleanup(host.ShutdownAll)
	enabled := true
	cfg := &config.Config{
		SDKConfig: sdkconfig.SDKConfig{APIKeys: []string{"native-caller-a", "native-caller-b"}},
		AuthDir:   dir,
		Plugins:   config.PluginsConfig{Enabled: true, Dir: dir, Configs: map[string]config.PluginInstanceConfig{"frontend": {Enabled: &enabled}}},
	}
	host.ApplyConfig(ctx, cfg)
	if !host.PluginRegistered("frontend") {
		t.Fatal("native frontend-only capability was not registered")
	}
	server := NewServer(cfg, auth.NewManager(nil, nil, nil), sdkaccess.NewManager(), filepath.Join(dir, "config.yaml"), WithPluginHost(host))
	httpServer := httptest.NewServer(server.engine)
	t.Cleanup(httpServer.Close)
	call := func(method, path, key string, body []byte) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, httpServer.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if key != "" {
			req.Header.Set("X-Goog-Api-Key", key)
		}
		req.Header["X-Multi"] = []string{"first", "second"}
		req.Header.Set("X-Caller-Scope", "forged")
		resp, err := httpServer.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
		return resp, raw
	}
	for _, key := range []string{"", "invalid"} {
		resp, _ := call(http.MethodPost, "/upload/v1beta/files", key, nil)
		if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("X-Native-Calls") != "" {
			t.Fatalf("unauthorized status=%d, plugin reached=%t", resp.StatusCode, resp.Header.Get("X-Native-Calls") != "")
		}
	}
	for _, path := range []string{"/healthz", "/v1beta/models", "/v1beta/models/missing", "/v1beta/interactions/missing"} {
		resp, _ := call(http.MethodGet, path, "native-caller-a", nil)
		if resp.Header.Get("X-Native-Calls") != "" {
			t.Fatal("plugin shadowed a standard route")
		}
	}
	const query = "q=one&q=two+words&caller_scope=forged"
	body := []byte{0, 255, '<', '&', '\n'}
	resp, raw := call(http.MethodPost, "/upload/v1beta/files?"+query, "native-caller-a", body)
	if resp.StatusCode != http.StatusCreated || resp.Header.Get("X-Native-Calls") != "1" {
		t.Fatalf("native upload status=%d, calls=%s", resp.StatusCode, resp.Header.Get("X-Native-Calls"))
	}
	if !reflect.DeepEqual(resp.Header.Values("X-Multi"), []string{"one", "two"}) {
		t.Fatal("response lost repeated headers")
	}
	var request struct {
		Method, Path, RawPath, RawQuery, RequestURI, Host string
		Headers                                           http.Header
		Query                                             map[string][]string
		Params                                            map[string]string
		Body                                              []byte
		CallerScope                                       string `json:"caller_scope"`
		CallbackID                                        string `json:"host_callback_id"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	if request.Method != http.MethodPost || request.Path != "/upload/v1beta/files" || request.RawQuery != query || request.RequestURI != request.Path+"?"+query || request.Host == "" || !bytes.Equal(request.Body, body) {
		t.Fatal("native request transport changed method, path, query, host, or binary body")
	}
	if !reflect.DeepEqual(request.Headers.Values("X-Multi"), []string{"first", "second"}) || !reflect.DeepEqual(request.Query["q"], []string{"one", "two words"}) {
		t.Fatal("native request lost repeated headers or query values")
	}
	if request.CallerScope != session.CallerScope("native-caller-a") || request.CallbackID == "" {
		t.Fatal("native request lacks trusted caller scope or callback context")
	}
	resp, raw = call(http.MethodPost, "/upload/v1beta/files/session-1", "native-caller-a", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload session status=%d", resp.StatusCode)
	}
	if err := json.Unmarshal(raw, &request); err != nil || request.Params["session"] != "session-1" {
		t.Fatal("upload session parameter missing")
	}
	resp, raw = call(http.MethodGet, "/v1beta/files/%66ile-1", "native-caller-a", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metadata status=%d", resp.StatusCode)
	}
	if err := json.Unmarshal(raw, &request); err != nil || request.Path != "/v1beta/files/file-1" || request.RawPath != "/v1beta/files/%66ile-1" || request.Params["id"] != "file-1" {
		t.Fatal("escaped path or dynamic parameter changed")
	}
	resp, raw = call(http.MethodGet, "/v1beta/files/file-1:download", "native-caller-a", nil)
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(raw, []byte{0, 255, '<', '&'}) {
		t.Fatal("native download status or binary response changed")
	}
	resp, _ = call(http.MethodGet, "/v1beta/files/file-1:download?caller_scope=forged", "native-caller-b", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatal("different API key could access the original caller's resource")
	}
	resp, _ = call(http.MethodGet, "/v1beta/files/file-1?bad_native_size=1", "native-caller-a", nil)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatal("invalid native buffer size was not rejected before copying")
	}
	for _, path := range []string{"/unknown", "/v1beta/../unsafe", "/v1beta/files/../file", "/v1beta/files/a%2Fb"} {
		resp, _ = call(http.MethodGet, path, "native-caller-a", nil)
		if resp.StatusCode != http.StatusNotFound || resp.Header.Get("X-Native-Calls") != "" {
			t.Fatal("unsafe or undeclared route reached plugin")
		}
	}
	host.ApplyConfig(ctx, cfg)
	server.RefreshPluginManagementRoutes()
	resp, _ = call(http.MethodPost, "/upload/v1beta/files", "native-caller-a", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatal("reconfigure retained removed route")
	}
	resp, _ = call(http.MethodGet, "/native/reconfigured", "native-caller-a", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatal("reconfigure did not publish replacement route")
	}
	anonymousConfig := *cfg
	anonymousConfig.SDKConfig = sdkconfig.SDKConfig{}
	anonymous := NewServer(&anonymousConfig, auth.NewManager(nil, nil, nil), sdkaccess.NewManager(), filepath.Join(dir, "anonymous.yaml"), WithPluginHost(host))
	anonymousResponse := httptest.NewRecorder()
	anonymous.engine.ServeHTTP(anonymousResponse, httptest.NewRequest(http.MethodGet, "/native/reconfigured?key=untrusted", nil))
	if anonymousResponse.Code != http.StatusUnauthorized || anonymousResponse.Header().Get("X-Native-Calls") != "" {
		t.Fatal("missing core authentication created an anonymous plugin namespace")
	}
	if !host.UnloadPlugin("frontend") {
		t.Fatal("native plugin was not loaded")
	}
	resp, _ = call(http.MethodGet, "/native/reconfigured", "native-caller-a", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatal("stopped plugin still has an active route")
	}
}
