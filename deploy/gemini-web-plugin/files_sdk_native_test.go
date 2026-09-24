//go:build cgo && (linux || darwin || freebsd)

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// This loads the actual shared library through core's native loader, route
// registration, API-key authentication and scoped authentication. The build
// overlay ONLY redirects Drive/OAuth to the stateful local HTTP fixture.
func TestFilesOfficialSDKNative(t *testing.T) {
	python := os.Getenv("CPA_FILES_SDK_PYTHON")
	if python == "" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	pluginSource, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(pluginSource, "../.."))
	work := t.TempDir()
	fixture := newFilesDriveFixture(t)
	sessions := filepath.Join(work, "sessions")
	svc := filesTestService(t, sessions)
	fixture.attach(svc)
	caller := filesDigest("cli-proxy-api:caller-scope:v1\x00files-caller-a")
	generated, err := svc.saveGeneratedFile(ctx, caller, "official-sdk-native-generated", "video/mp4", []byte("generated-sdk-media"))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.shutdownSessions(); err != nil {
		t.Fatal(err)
	}
	pluginDir := filepath.Join(work, "plugins", runtime.GOOS, runtime.GOARCH)
	if err := os.MkdirAll(pluginDir, 0700); err != nil {
		t.Fatal(err)
	}
	ext := ".so"
	if runtime.GOOS == "darwin" {
		ext = ".dylib"
	}
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	override := filepath.Join(work, "native_fixture.go")
	write(override, []byte(fmt.Sprintf("package main\nfunc init() { pluginService.driveOverride = %q; pluginService.driveTokenOverride = %q }\n", fixture.server.URL, fixture.server.URL+"/token")))
	overlay := filepath.Join(work, "overlay.json")
	write(overlay, jsonFixture(t, map[string]any{"Replace": map[string]string{filepath.Join(pluginSource, "files_native_fixture.go"): override}}))
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-overlay", overlay, "-buildmode=c-shared", "-o", filepath.Join(pluginDir, "gemini-web"+ext), ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build actual native plugin: %v\n%s", err, output)
	}
	// Go's internal import rule requires the ephemeral runner inside the root
	// module. No core source, deployed artifact or production config is changed.
	runnerDir, err := os.MkdirTemp(root, ".files-native-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(runnerDir); err != nil {
			t.Error(err)
		}
	}()
	write(filepath.Join(runnerDir, "main.go"), []byte(filesNativeRunner))
	runner := filepath.Join(work, "core-fixture")
	build = exec.CommandContext(ctx, "go", "build", "-p=4", "-o", runner, ".")
	build.Dir = runnerDir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build core runner: %v\n%s", err, output)
	}
	config, err := yaml.Marshal(map[string]any{
		"api-keys": []string{"files-caller-a", "files-caller-b"}, "auth-dir": filepath.Join(work, "auths"),
		"plugins": map[string]any{"enabled": true, "dir": filepath.Join(work, "plugins"), "configs": map[string]any{"gemini-web": map[string]any{
			"enabled": true, "session_dir": sessions, "manager_origin": "https://cpa.example", "browser_extension_id": strings.Repeat("a", 32),
			"drive_client_id": "files-test-client", "drive_client_secret": "files-test-secret", "drive_refresh_token": "files-test-refresh",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(work, "config.yaml")
	write(configPath, config)
	command := exec.CommandContext(ctx, runner, configPath)
	command.Env = append(os.Environ(), "GEMINI_WEB_SESSION_KEY="+sessionKeyFixture())
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.Create(filepath.Join(work, "core-stderr.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := stderr.Close(); err != nil {
			t.Error(err)
		}
	}()
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := stdin.Close(); err != nil {
			t.Error(err)
		}
		if err := command.Wait(); err != nil {
			t.Errorf("core runner failed: %v", err)
		}
	}()
	ready := make(chan string, 1)
	readErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if address, found := strings.CutPrefix(scanner.Text(), "FILES_NATIVE_READY "); found {
				ready <- address
			}
		}
		readErr <- scanner.Err()
	}()
	var address string
	select {
	case address = <-ready:
	case err := <-readErr:
		detail, readErr := os.ReadFile(stderr.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		t.Fatalf("core runner ended before readiness: %v\n%s", err, detail)
	case <-ctx.Done():
		t.Fatal("core runner did not announce readiness")
	}
	for _, key := range []string{"", "invalid"} {
		status, _, _ := filesTestRequest(t, "POST", address+"/upload/v1beta/files", key, filesStartHeaders(3), []byte(`{"file":{}}`))
		if status != 401 {
			t.Fatalf("native start without valid core API key: %d", status)
		}
	}
	sdk := exec.CommandContext(ctx, python, "-c", filesSDKProgram, address, generated.Name)
	if output, err := sdk.CombinedOutput(); err != nil {
		t.Fatalf("official SDK through core/native failed: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
	status, _, raw := filesTestRequest(t, "GET", address+"/__files_fixture/stats", "", nil, nil)
	var stats struct{ Starts, AnonymousChunks int }
	if status != 200 || json.Unmarshal(raw, &stats) != nil || stats.Starts != 1 || stats.AnonymousChunks != 2 {
		t.Fatalf("SDK did not send one API-key start and two credential-free chunks: %s", raw)
	}
	// Possession always maps to the original owner, even with another valid key.
	location := filesTestStart(t, address, "native-capability", 3)
	status, _, raw = filesTestCommand(t, location, "files-caller-b", "upload, finalize", 0, []byte("abc"))
	if status != 200 {
		t.Fatalf("native possessed capability upload failed: status=%d body=%s", status, raw)
	}
	status, _, _ = filesTestRequest(t, "GET", address+"/v1beta/files/native-capability", "files-caller-a", nil, nil)
	if status != 200 {
		t.Fatal("native capability did not preserve core-derived owner")
	}
	status, _, _ = filesTestRequest(t, "GET", address+"/v1beta/files/native-capability", "files-caller-b", nil, nil)
	if status != 404 {
		t.Fatal("native capability reassigned resource ownership")
	}
	fixture.mu.Lock()
	requests := fixture.requests
	fixture.mu.Unlock()
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{location + "&upload_id=" + parsed.Query().Get("upload_id"), address + filesResumablePath + "?upload_id=invalid"} {
		status, headers, _ := filesTestCommand(t, invalid, "files-caller-a", "query", 0, nil)
		if status != 401 || headers.Get("X-Goog-Upload-Status") != "cancelled" {
			t.Fatal("native invalid capability fell back to core API-key authentication")
		}
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.requests != requests {
		t.Fatal("native invalid capability reached Drive")
	}
}

const filesNativeRunner = `package main

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http/httptest"
    "os"
    "sync/atomic"
    "github.com/gin-gonic/gin"
    "github.com/router-for-me/CLIProxyAPI/v7/internal/api"
    "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
    "github.com/router-for-me/CLIProxyAPI/v7/internal/pluginhost"
    sdkaccess "github.com/router-for-me/CLIProxyAPI/v7/sdk/access"
    "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
    log "github.com/sirupsen/logrus"
    "gopkg.in/yaml.v3"
)

func run() error {
    log.SetLevel(log.ErrorLevel)
    raw, err := os.ReadFile(os.Args[1]); if err != nil { return err }
    var cfg config.Config
    if err := yaml.Unmarshal(raw, &cfg); err != nil { return err }
    host := pluginhost.New()
    defer host.ShutdownAll()
    host.ApplyConfig(context.Background(), &cfg)
    if !host.PluginRegistered("gemini-web") { return fmt.Errorf("actual native plugin not registered") }
    var engine *gin.Engine
    var starts, chunks atomic.Int64
    api.NewServer(&cfg, auth.NewManager(nil, nil, nil), sdkaccess.NewManager(), os.Args[1], api.WithPluginHost(host), api.WithEngineConfigurator(func(e *gin.Engine) {
        engine = e
        e.Use(func(c *gin.Context) {
            r := c.Request
            if r.Method == "POST" && r.URL.Path == "/upload/v1beta/files" && r.Header.Get("X-Goog-Api-Key") == "files-caller-a" { starts.Add(1) }
            if r.Method == "POST" && r.URL.Path == "/upload/v1beta/files/resumable" && r.Header.Get("X-Goog-Api-Key") == "" && r.Header.Get("Authorization") == "" { chunks.Add(1) }
            c.Next()
        })
        e.GET("/__files_fixture/stats", func(c *gin.Context) {
            c.JSON(200, map[string]int64{"Starts": starts.Load(), "AnonymousChunks": chunks.Load()})
        })
    }))
    server := httptest.NewServer(engine)
    defer server.Close()
    fmt.Println("FILES_NATIVE_READY " + server.URL)
    if _, err := io.Copy(io.Discard, os.Stdin); err != nil { return err }
    return nil
}

func main() {
    if err := run(); err != nil {
        if err := json.NewEncoder(os.Stderr).Encode(map[string]string{"error": err.Error()}); err != nil { os.Exit(2) }
        os.Exit(1)
    }
}
`
