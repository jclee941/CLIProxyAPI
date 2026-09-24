package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func filesStartHeaders(size int) http.Header {
	return http.Header{
		"Content-Type": {"application/json"}, "X-Goog-Upload-Protocol": {"resumable"}, "X-Goog-Upload-Command": {"start"},
		"X-Goog-Upload-Header-Content-Length": {strconv.Itoa(size)}, "X-Goog-Upload-Header-Content-Type": {"video/mp4"},
	}
}

func TestFilesRequireDriveWriteOAuthNotAPIKey(t *testing.T) {
	for _, variable := range []string{"GOOGLE_DRIVE_CLIENT_ID", "GOOGLE_DRIVE_CLIENT_SECRET", "GOOGLE_DRIVE_REFRESH_TOKEN"} {
		t.Setenv(variable, "")
	}
	svc := filesTestService(t, filepath.Join(t.TempDir(), "sessions"))
	svc.config.DriveAPIKey = "read-only-test-key"
	fixture := newFilesDriveFixture(t)
	svc.driveOverride = fixture.server.URL
	server := filesTestHTTP(t, svc)
	status, _, body := filesTestRequest(t, "POST", server.URL+"/upload/v1beta/files", "files-caller-a", filesStartHeaders(4), []byte(`{"file":{}}`))
	if status != 503 || !bytes.Contains(body, []byte("files_requires_drive_write_oauth")) {
		t.Fatalf("API-key-only write status=%d body=%s", status, body)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.requests != 0 {
		t.Fatal("missing OAuth attempted a Drive operation")
	}
}

func TestFilesValidateTransportAndMetadataBeforeDrive(t *testing.T) {
	svc := filesTestService(t, filepath.Join(t.TempDir(), "sessions"))
	fixture := newFilesDriveFixture(t)
	fixture.attach(svc)
	server := filesTestHTTP(t, svc)
	for _, test := range []struct {
		name, body string
		size       int
		status     int
	}{
		{"too large", `{"file":{}}`, filesMaxBytes + 1, 413},
		{"negative", `{"file":{}}`, -1, 400},
		{"size mismatch", `{"file":{"size_bytes":5}}`, 4, 400},
		{"mime mismatch", `{"file":{"mime_type":"text/plain"}}`, 4, 400},
		{"path traversal", `{"file":{"name":"files/../other"}}`, 4, 400},
		{"remote reference", `{"file":{"name":"https://untrusted.invalid/file"}}`, 4, 400},
		{"output injection", `{"file":{"downloadUri":"https://untrusted.invalid/file"}}`, 4, 400},
		{"long display", `{"file":{"display_name":"` + strings.Repeat("d", 513) + `"}}`, 4, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, _, body := filesTestRequest(t, "POST", server.URL+"/upload/v1beta/files", "files-caller-a", filesStartHeaders(test.size), []byte(test.body))
			if status != test.status {
				t.Fatalf("status=%d body=%s", status, body)
			}
		})
	}
	result := invoke(t, svc, "frontend_http.handle", map[string]any{"Method": "GET", "Path": "/v1beta/files", "Headers": map[string][]string{"Caller-Scope": {filesTestCaller}}, "Metadata": map[string]string{"caller_scope": filesTestCaller}})
	var response httpResponse
	if !result.OK || json.Unmarshal(result.Result, &response) != nil || response.StatusCode != 401 {
		t.Fatal("headers or executor metadata impersonated trusted frontend caller_scope")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.requests != 0 {
		t.Fatal("invalid Files request reached Drive")
	}
}

func TestFilesResumableSeparateFinalizeAndChunkValidation(t *testing.T) {
	svc := filesTestService(t, filepath.Join(t.TempDir(), "sessions"))
	newFilesDriveFixture(t).attach(svc)
	server := filesTestHTTP(t, svc)
	location := filesTestStart(t, server.URL, "separate", 5)
	for _, test := range []struct {
		command string
		offset  int
		body    []byte
	}{
		{"upload", 0, []byte("a")},
		{"upload, finalize", 0, []byte("a")},
		{"upload", -1, []byte("abcde")},
		{"query", 0, []byte("a")},
		{"finalize", 0, []byte("a")},
		{"invalid", 0, nil},
	} {
		status, _, raw := filesTestCommand(t, location, "files-caller-a", test.command, test.offset, test.body)
		if status != 400 {
			t.Fatalf("invalid command accepted: %s status=%d body=%s", test.command, status, raw)
		}
	}
	status, _, _ := filesTestCommand(t, location, "files-caller-a", "upload", 0, make([]byte, filesChunkBytes+1))
	if status != 413 {
		t.Fatalf("oversized RPC chunk accepted: %d", status)
	}
	status, headers, body := filesTestCommand(t, location, "files-caller-a", "upload", 0, []byte("abcde"))
	if status != 200 || headers.Get("X-Goog-Upload-Status") != "active" {
		t.Fatalf("upload finalized prematurely: status=%d body=%s", status, body)
	}
	status, headers, body = filesTestCommand(t, location, "files-caller-a", "finalize", 5, nil)
	if status != 200 || headers.Get("X-Goog-Upload-Status") != "final" {
		t.Fatalf("separate finalize status=%d body=%s", status, body)
	}
	location = filesTestStart(t, server.URL, "empty", 0)
	status, headers, body = filesTestCommand(t, location, "files-caller-a", "upload, finalize", 0, nil)
	if status != 200 || headers.Get("X-Goog-Upload-Status") != "final" || !bytes.Contains(body, []byte(`"sizeBytes":"0"`)) {
		t.Fatalf("empty file status=%d body=%s", status, body)
	}
}

func TestFilesPageTokensAreCallerBound(t *testing.T) {
	svc := filesTestService(t, filepath.Join(t.TempDir(), "sessions"))
	newFilesDriveFixture(t).attach(svc)
	server := filesTestHTTP(t, svc)
	for _, name := range []string{"a", "b"} {
		location := filesTestStart(t, server.URL, name, 1)
		status, _, raw := filesTestCommand(t, location, "files-caller-a", "upload, finalize", 0, []byte("v"))
		if status != 200 {
			t.Fatalf("upload status=%d body=%s", status, raw)
		}
	}
	status, _, raw := filesTestRequest(t, "GET", server.URL+"/v1beta/files?pageSize=1", "files-caller-a", nil, nil)
	var page struct {
		Files []fileResource `json:"files"`
		Next  string         `json:"nextPageToken"`
	}
	if status != 200 || json.Unmarshal(raw, &page) != nil || len(page.Files) != 1 || page.Files[0].Name != "files/a" || page.Next == "" {
		t.Fatalf("first page status=%d body=%s", status, raw)
	}
	location := server.URL + "/v1beta/files?pageSize=1&pageToken=" + url.QueryEscape(page.Next)
	status, _, raw = filesTestRequest(t, "GET", location, "files-caller-b", nil, nil)
	if status != 400 {
		t.Fatalf("foreign page token status=%d body=%s", status, raw)
	}
	status, _, raw = filesTestRequest(t, "GET", location, "files-caller-a", nil, nil)
	if status != 200 || !bytes.Contains(raw, []byte(`"name":"files/b"`)) || bytes.Contains(raw, []byte("nextPageToken")) {
		t.Fatalf("second page status=%d body=%s", status, raw)
	}
}

func TestFilesDriveWriteDenialAndLocationStayPrivate(t *testing.T) {
	for _, kind := range []string{"permission", "location"} {
		t.Run(kind, func(t *testing.T) {
			svc := filesTestService(t, filepath.Join(t.TempDir(), "sessions"))
			fixture := newFilesDriveFixture(t)
			fixture.attach(svc)
			fixture.denyWrites, fixture.badLocation = kind == "permission", kind == "location"
			server := filesTestHTTP(t, svc)
			status, headers, body := filesTestRequest(t, "POST", server.URL+"/upload/v1beta/files", "files-caller-a", filesStartHeaders(4), []byte(`{"file":{}}`))
			if status != 503 && status != 502 || headers.Get("X-Goog-Upload-URL") != "" || bytes.Contains(body, []byte("privateDrive")) || bytes.Contains(body, []byte("untrusted.invalid")) || bytes.Contains(body, []byte("files-test-")) {
				t.Fatalf("unsafe Drive failure response: status=%d body=%s", status, body)
			}
		})
	}
}

func TestFilesDeletionIntentSurvivesDriveFailure(t *testing.T) {
	svc := filesTestService(t, filepath.Join(t.TempDir(), "sessions"))
	fixture := newFilesDriveFixture(t)
	fixture.attach(svc)
	file, err := svc.saveGeneratedFile(context.Background(), filesTestCaller, "delete-retry", "video/mp4", []byte("generated"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	fixture.failDelete = true
	fixture.mu.Unlock()
	server := filesTestHTTP(t, svc)
	location := server.URL + "/v1beta/" + file.Name
	status, _, _ := filesTestRequest(t, "DELETE", location, "files-caller-a", nil, nil)
	if status != 502 {
		t.Fatalf("failed Drive delete reported success: %d", status)
	}
	status, _, _ = filesTestRequest(t, "GET", location, "files-caller-a", nil, nil)
	if status != 404 {
		t.Fatalf("pending deletion still served: %d", status)
	}
	status, _, _ = filesTestRequest(t, "DELETE", location, "files-caller-a", nil, nil)
	if status != 200 {
		t.Fatalf("idempotent delete retry failed: %d", status)
	}
}

func TestFilesMetadataFailurePreventsUntrackedDriveCreate(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "sessions")
	svc := filesTestService(t, directory)
	fixture := newFilesDriveFixture(t)
	fixture.attach(svc)
	svc.sessions.fault = func(stage string) error {
		if stage == "file_sync" {
			return errors.New("fixture disk failure")
		}
		return nil
	}
	_, err := svc.saveGeneratedFile(context.Background(), filesTestCaller, "faulted", "video/mp4", []byte("bytes"))
	if err == nil {
		t.Fatal("failed metadata commit reported success")
	}
	fixture.mu.Lock()
	if fixture.writes != 0 {
		t.Error("Drive object created before its local ID was durable")
	}
	fixture.mu.Unlock()
	if _, err := os.Stat(filepath.Join(directory, "write.intent")); err != nil {
		t.Fatal("ambiguous metadata transaction lost its recovery fence")
	}
}

func TestFilesConcurrentSameNameCreatesOneDriveObject(t *testing.T) {
	svc := filesTestService(t, filepath.Join(t.TempDir(), "sessions"))
	fixture := newFilesDriveFixture(t)
	fixture.attach(svc)
	server := filesTestHTTP(t, svc)
	const clients = 8
	results := make(chan int, clients)
	for range clients {
		go func() {
			status, _, _ := filesTestRequest(t, "POST", server.URL+"/upload/v1beta/files", "files-caller-a", filesStartHeaders(4), []byte(`{"file":{"name":"files/concurrent"}}`))
			results <- status
		}()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	successes := 0
	for range clients {
		select {
		case status := <-results:
			if status == 200 {
				successes++
			} else if status != 409 {
				t.Fatalf("concurrent start status=%d", status)
			}
		case <-ctx.Done():
			t.Fatal("concurrent Files start did not complete")
		}
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if successes != 1 || fixture.allocated != 1 || fixture.writes != 1 {
		t.Fatalf("duplicate remote create: successes=%d allocated=%d writes=%d", successes, fixture.allocated, fixture.writes)
	}
}

func TestFilesRegistrationUsesAuthenticatedPublicRoutes(t *testing.T) {
	svc := newService(nil)
	registration := invoke(t, svc, "plugin.register", nil)
	var registered struct {
		Schema       int `json:"schema_version"`
		Capabilities struct {
			FrontendHTTP bool `json:"frontend_http"`
			GlobalAuth   bool `json:"frontend_auth_provider"`
		} `json:"capabilities"`
	}
	if !registration.OK || json.Unmarshal(registration.Result, &registered) != nil || registered.Schema != 6 || !registered.Capabilities.FrontendHTTP || registered.Capabilities.GlobalAuth {
		t.Fatal("native frontend HTTP capability was not registered")
	}
	routes := invoke(t, svc, "frontend_http.register", nil)
	var response struct {
		Routes []struct{ Method, Path, AuthMode string }
	}
	if !routes.OK || json.Unmarshal(routes.Result, &response) != nil || len(response.Routes) != 6 {
		t.Fatal("Files RPC routes missing")
	}
	want := map[string]string{"POST /upload/v1beta/files": "", "POST /upload/v1beta/files/resumable": "scoped", "GET /v1beta/files": "", "GET /v1beta/files/{id}": "", "DELETE /v1beta/files/{id}": "", "GET /v1beta/files/{id}:download": ""}
	for _, route := range response.Routes {
		if mode, found := want[route.Method+" "+route.Path]; !found || mode != route.AuthMode {
			t.Fatalf("unexpected public Files route: %+v", route)
		}
		delete(want, route.Method+" "+route.Path)
	}
	if len(want) != 0 {
		t.Fatalf("missing Files routes: %v", want)
	}
}
