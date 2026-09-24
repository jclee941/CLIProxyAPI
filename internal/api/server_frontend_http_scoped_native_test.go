//go:build cgo && (linux || darwin || freebsd)

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pluginhost"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v7/sdk/access"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/session"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestFrontendHTTPNativeScopedAuthentication(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, runtime.GOOS, runtime.GOARCH)
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatal(err)
	}
	base, err := filepath.Abs("testdata/frontend_http.c")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "scoped.c")
	if err := os.WriteFile(source, []byte(fmt.Sprintf(nativeScopedFixture, base)), 0o600); err != nil {
		t.Fatal(err)
	}
	ext := ".so"
	if runtime.GOOS == "darwin" {
		ext = ".dylib"
	}
	command := exec.CommandContext(ctx, "cc", "-shared", "-fPIC", "-pthread", "-Wall", "-Wextra", "-Werror", "-o", filepath.Join(pluginDir, "scoped"+ext), source)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compile scoped fixture: %v\n%s", err, output)
	}
	host := pluginhost.New()
	t.Cleanup(host.ShutdownAll)
	enabled := true
	cfg := &config.Config{
		SDKConfig: sdkconfig.SDKConfig{APIKeys: []string{"scoped-caller-a", "scoped-caller-b"}, RequestLog: true},
		AuthDir:   dir,
		Plugins:   config.PluginsConfig{Enabled: true, Dir: dir, Configs: map[string]config.PluginInstanceConfig{"scoped": {Enabled: &enabled}}},
	}
	host.ApplyConfig(ctx, cfg)
	if !host.PluginRegistered("scoped") {
		t.Fatal("native scoped fixture not registered")
	}
	server := NewServer(cfg, auth.NewManager(nil, nil, nil), sdkaccess.NewManager(), filepath.Join(dir, "config.yaml"), WithPluginHost(host))
	httpServer := httptest.NewServer(server.engine)
	t.Cleanup(httpServer.Close)
	call := func(path, key string, body []byte) (int, pluginapi.FrontendHTTPRequest) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if key != "" {
			req.Header.Set("X-Goog-Api-Key", key)
		}
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
		var received pluginapi.FrontendHTTPRequest
		if resp.StatusCode == http.StatusCreated {
			if err := json.Unmarshal(raw, &received); err != nil {
				t.Fatal(err)
			}
		}
		return resp.StatusCode, received
	}
	status, started := call("/upload/v1beta/files", "scoped-caller-a", nil)
	if status != http.StatusCreated || started.CallerScope != session.CallerScope("scoped-caller-a") {
		t.Fatalf("upload start status=%d, scope=%q", status, started.CallerScope)
	}
	const resumable = "/upload/v1beta/files/resumable"
	body := []byte{0, 255, '<', '&'}
	for _, key := range []string{"", "scoped-caller-b", "invalid"} {
		status, resumed := call(resumable+"?upload_id=opaque-session&caller_scope=forged", key, body)
		if status != http.StatusCreated || resumed.CallerScope != started.CallerScope || !bytes.Equal(resumed.Body, body) {
			t.Fatalf("bearer resume with API key %q: status=%d scope=%q body=%v", key, status, resumed.CallerScope, resumed.Body)
		}
	}
	for _, test := range []struct {
		query  string
		status int
	}{
		{"", 401}, {"?upload_id=tampered", 401}, {"?upload_id=opaque-session-extra", 401},
		{"?upload_id=expired", 410}, {"?upload_id=error", 401},
	} {
		t.Run(test.query, func(t *testing.T) {
			body := &frontendUnreadBody{}
			req := httptest.NewRequest(http.MethodPost, resumable+test.query, body)
			req.ContentLength = pluginapi.FrontendHTTPMaxBodyBytes + 1
			req.Header.Set("X-Goog-Api-Key", "scoped-caller-a")
			response := httptest.NewRecorder()
			server.engine.ServeHTTP(response, req)
			if response.Code != test.status || body.reads != 0 || response.Header().Get("X-Native-Calls") != "" {
				t.Fatalf("rejected resume status=%d reads=%d headers=%v", response.Code, body.reads, response.Header())
			}
			if test.status == 410 && (response.Body.String() != "expired" || response.Header().Get("X-Upload-Rejected") != "true") {
				t.Fatal("native rejection response not preserved")
			}
		})
	}
	for _, path := range []string{"/upload/v1beta/files", "/v1beta/files/file-1", "/v1beta/models"} {
		method := http.MethodGet
		if strings.HasPrefix(path, "/upload/") {
			method = http.MethodPost
		}
		response := httptest.NewRecorder()
		server.engine.ServeHTTP(response, httptest.NewRequest(method, path+"?upload_id=opaque-session", nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("bearer token bypassed core auth for %s: %d", path, response.Code)
		}
	}
}

type frontendUnreadBody struct{ reads int }

func (b *frontendUnreadBody) Read([]byte) (int, error) {
	b.reads++
	return 0, io.EOF
}

// Reuse only the base fixture's ABI, buffering, and handler transport. The plugin
// deliberately does not advertise frontend_auth_provider: auth is route-local.
const nativeScopedFixture = `
#define call base_call
#define cliproxy_plugin_init base_init
#include %q
#undef call
#undef cliproxy_plugin_init

static int scoped_call(const char *method, const uint8_t *raw, size_t n, buffer *out) {
    if (!strcmp(method, "frontend_http.register")) {
        return result(out, "{\"ok\":true,\"result\":{\"Routes\":["
            "{\"Method\":\"POST\",\"Path\":\"/upload/v1beta/files\"},"
            "{\"Method\":\"POST\",\"Path\":\"/upload/v1beta/files/resumable\",\"AuthMode\":\"scoped\"},"
            "{\"Method\":\"GET\",\"Path\":\"/v1beta/files/{id}\"}]}}");
    }
    if (!strcmp(method, "frontend_auth.authenticate")) {
        char *req = malloc(n + 1);
        if (!req) return 1;
        memcpy(req, raw, n);
        req[n] = 0;
        int valid = strstr(req, "\"upload_id\":[\"opaque-session\"]") != NULL;
        int expired = strstr(req, "\"upload_id\":[\"expired\"]") != NULL;
        int failed = strstr(req, "\"upload_id\":[\"error\"]") != NULL;
        int body_free = strstr(req, "\"Body\":null") != NULL;
        free(req);
        if (failed || !body_free) return 1;
        if (expired) return result(out, "{\"ok\":true,\"result\":{\"Rejection\":{\"StatusCode\":410,\"Headers\":{\"X-Upload-Rejected\":[\"true\"]},\"Body\":\"ZXhwaXJlZA==\"}}}");
        if (!valid) return result(out, "{\"ok\":true,\"result\":{\"Authenticated\":false}}");
        char response[256];
        snprintf(response, sizeof(response), "{\"ok\":true,\"result\":{\"Authenticated\":true,\"caller_scope\":\"%%s\"}}", owner);
        return result(out, response);
    }
    return base_call(method, raw, n, out);
}

int cliproxy_plugin_init(const host_api *host, plugin_api *plugin) {
    int code = base_init(host, plugin);
    if (!code) plugin->base_call = scoped_call;
    return code;
}
`
