package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func filesTestAuthenticate(t *testing.T, svc *service, request any) filesUploadAuthentication {
	t.Helper()
	reply := invoke(t, svc, "frontend_auth.authenticate", request)
	var auth filesUploadAuthentication
	if !reply.OK || json.Unmarshal(reply.Result, &auth) != nil || auth.Authenticated == (auth.Rejection != nil) {
		t.Fatalf("invalid authentication RPC result: %s", reply.Result)
	}
	return auth
}

func filesTestCapability(t *testing.T) (*service, *filesDriveFixture, filesUploadAuthRequest) {
	t.Helper()
	svc := filesTestService(t, filepath.Join(t.TempDir(), "sessions"))
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	fixture := newFilesDriveFixture(t)
	fixture.attach(svc)
	response, err := svc.filesRoute(t.Context(), filesHTTPRequest{
		Method: "POST", Path: "/upload/v1beta/files", Scheme: "https", Host: "cpa.example",
		CallerScope: filesTestCaller, Headers: filesStartHeaders(3), Body: []byte(`{"file":{"name":"files/capability"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	location, err := url.Parse(response.Headers.Get("X-Goog-Upload-URL"))
	if err != nil || location.Path != filesResumablePath || location.Host != "cpa.example" {
		t.Fatal("invalid capability URL")
	}
	return svc, fixture, filesUploadAuthRequest{Method: "POST", Path: location.Path, RawQuery: location.RawQuery, Query: location.Query()}
}

func filesTestUnchanged(t *testing.T, svc *service, fixture *filesDriveFixture) func() {
	t.Helper()
	path := filepath.Join(svc.localStore().directory.Name(), filesMetadataName(filesTestCaller, "capability"))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	requests := fixture.requests
	fixture.mu.Unlock()
	return func() {
		t.Helper()
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("verification mutated encrypted metadata")
		}
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		if fixture.requests != requests {
			t.Fatalf("verification reached Drive: before=%d after=%d", requests, fixture.requests)
		}
	}
}

func filesTestAuthDenied(t *testing.T, auth filesUploadAuthentication, status int) {
	t.Helper()
	if auth.Authenticated || auth.CallerScope != "" || auth.Rejection == nil || auth.Rejection.StatusCode != status {
		t.Fatalf("unexpected capability authentication: %+v", auth)
	}
	response := auth.Rejection
	var body struct {
		Error struct {
			Code   int    `json:"code"`
			Status string `json:"status"`
		} `json:"error"`
	}
	if response.Headers.Get("X-Goog-Upload-Status") != "cancelled" || response.Headers.Get("Cache-Control") != "private, no-store" || json.Unmarshal(response.Body, &body) != nil || body.Error.Code != status || body.Error.Status == "" {
		t.Fatalf("capability rejection is not a terminal Google upload error: %+v", response)
	}
}

func filesTestSealClaims(t *testing.T, svc *service, raw []byte, aad string) string {
	t.Helper()
	store := svc.localStore()
	store.mu.Lock()
	defer store.mu.Unlock()
	nonce := make([]byte, store.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(store.aead.Seal(nonce, nonce, raw, []byte(aad)))
}

func TestFilesUploadCapabilityMinimalClaimsAndBodyFreeAuthentication(t *testing.T) {
	svc, fixture, request := filesTestCapability(t)
	unchanged := filesTestUnchanged(t, svc, fixture)
	token := request.Query.Get("upload_id")
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(token) > 512 || base64.RawURLEncoding.EncodeToString(raw) != token {
		t.Fatal("capability is not bounded canonical base64url")
	}
	store := svc.localStore()
	n := store.aead.NonceSize()
	plaintext, err := store.aead.Open(nil, raw[:n], raw[n:], []byte("gemini-web-files-upload-capability-v1\x00POST\x00/upload/v1beta/files/resumable"))
	if err != nil {
		t.Fatal("capability is not encrypted with the persistent AEAD and route-bound AAD")
	}
	var claims map[string]json.RawMessage
	var parsed filesUploadClaims
	if json.Unmarshal(plaintext, &claims) != nil || len(claims) != 3 || strictJSON(plaintext, &parsed) != nil || parsed.CallerScope != filesTestCaller || parsed.FileID != "capability" || parsed.ExpiresAt != svc.now().Add(24*time.Hour).Unix() {
		t.Fatal("capability includes nonminimal or invalid claims")
	}
	for _, secret := range []string{filesTestCaller, "files-caller-a", "privateDrive", "files-test", "cpa.example", fixture.server.URL} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("capability exposes private plaintext")
		}
	}
	// A non-base64 Body proves the verifier does not decode the upload bytes.
	auth := filesTestAuthenticate(t, svc, map[string]any{
		"Method": request.Method, "Path": request.Path, "Query": request.Query, "RawQuery": request.RawQuery,
		"Body": map[string]any{"not": "an upload body"}, "caller_scope": strings.Repeat("b", 64),
		"Headers": http.Header{"Authorization": {"Bearer files-caller-b"}, "X-Goog-Api-Key": {"files-caller-b"}, "Caller-Scope": {strings.Repeat("b", 64)}},
	})
	if !auth.Authenticated || auth.CallerScope != filesTestCaller {
		t.Fatal("capability did not map to its original owner")
	}
	unchanged()
}

func TestFilesUploadCapabilityRejectsInvalidWithoutDrive(t *testing.T) {
	svc, fixture, request := filesTestCapability(t)
	unchanged := filesTestUnchanged(t, svc, fixture)
	token := request.Query.Get("upload_id")
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 1
	tampered := base64.RawURLEncoding.EncodeToString(raw)
	store := svc.localStore()
	store.mu.Lock()
	page, err := store.filesTokenLocked(filesTestCaller, "page", "files/capability")
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*filesUploadAuthRequest)
	}{
		{"method", func(r *filesUploadAuthRequest) { r.Method = "PUT" }},
		{"method case", func(r *filesUploadAuthRequest) { r.Method = "post" }},
		{"start path", func(r *filesUploadAuthRequest) { r.Path = "/upload/v1beta/files" }},
		{"list path", func(r *filesUploadAuthRequest) { r.Path = "/v1beta/files" }},
		{"download path", func(r *filesUploadAuthRequest) { r.Path = "/v1beta/files/capability:download" }},
		{"path suffix", func(r *filesUploadAuthRequest) { r.Path += "/" }},
		{"encoded path", func(r *filesUploadAuthRequest) { r.RawPath = "/upload/v1beta/files/%72esumable" }},
		{"missing", func(r *filesUploadAuthRequest) { r.Query = nil }},
		{"duplicate", func(r *filesUploadAuthRequest) { r.Query["upload_id"] = []string{token, token} }},
		{"empty duplicate", func(r *filesUploadAuthRequest) { r.Query["upload_id"] = []string{token, ""} }},
		{"raw duplicate", func(r *filesUploadAuthRequest) { r.RawQuery = request.RawQuery + "&upload_id=" + token }},
		{"encoded duplicate", func(r *filesUploadAuthRequest) { r.RawQuery = request.RawQuery + "&%75pload_id=" + token }},
		{"raw malformed", func(r *filesUploadAuthRequest) { r.RawQuery += "&bad=%" }},
		{"raw mismatch", func(r *filesUploadAuthRequest) { r.RawQuery = "upload_id=other" }},
		{"empty", func(r *filesUploadAuthRequest) { r.Query.Set("upload_id", "") }},
		{"large", func(r *filesUploadAuthRequest) { r.Query.Set("upload_id", strings.Repeat("a", 513)) }},
		{"tamper", func(r *filesUploadAuthRequest) { r.Query.Set("upload_id", tampered) }},
		{"padding", func(r *filesUploadAuthRequest) { r.Query.Set("upload_id", token+"=") }},
		{"newline", func(r *filesUploadAuthRequest) { r.Query.Set("upload_id", token+"\n") }},
		{"truncated", func(r *filesUploadAuthRequest) { r.Query.Set("upload_id", token[:12]) }},
		{"wrong purpose", func(r *filesUploadAuthRequest) { r.Query.Set("upload_id", page) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := request
			changed.Query = url.Values{"upload_id": {token}}
			changed.RawQuery = ""
			test.change(&changed)
			filesTestAuthDenied(t, filesTestAuthenticate(t, svc, changed), 401)
		})
	}
	filesTestAuthDenied(t, filesTestAuthenticate(t, svc, nil), 401)
	unchanged()
}

func TestFilesUploadCapabilityRejectsInvalidClaimsAndPurpose(t *testing.T) {
	svc, fixture, request := filesTestCapability(t)
	unchanged := filesTestUnchanged(t, svc, fixture)
	valid := filesUploadClaims{CallerScope: filesTestCaller, FileID: "capability", ExpiresAt: svc.now().Add(24 * time.Hour).Unix()}
	for _, test := range []struct {
		name   string
		change func(*filesUploadClaims)
		status int
	}{
		{"missing scope", func(c *filesUploadClaims) { c.CallerScope = "" }, 401},
		{"uppercase scope", func(c *filesUploadClaims) { c.CallerScope = strings.Repeat("A", 64) }, 401},
		{"short scope", func(c *filesUploadClaims) { c.CallerScope = "a" }, 401},
		{"foreign scope", func(c *filesUploadClaims) { c.CallerScope = strings.Repeat("b", 64) }, 404},
		{"missing file", func(c *filesUploadClaims) { c.FileID = "absent" }, 404},
		{"traversal", func(c *filesUploadClaims) { c.FileID = "../capability" }, 401},
		{"file suffix", func(c *filesUploadClaims) { c.FileID = "capability-" }, 401},
		{"file length", func(c *filesUploadClaims) { c.FileID = strings.Repeat("a", 41) }, 401},
		{"missing expiry", func(c *filesUploadClaims) { c.ExpiresAt = 0 }, 401},
		{"negative expiry", func(c *filesUploadClaims) { c.ExpiresAt = -1 }, 401},
		{"expired", func(c *filesUploadClaims) { c.ExpiresAt = svc.now().Unix() }, 410},
		{"excess lifetime", func(c *filesUploadClaims) { c.ExpiresAt++ }, 401},
	} {
		t.Run(test.name, func(t *testing.T) {
			claims := valid
			test.change(&claims)
			token := filesTestSealClaims(t, svc, jsonFixture(t, claims), filesUploadAAD)
			changed := request
			changed.RawQuery, changed.Query = "", url.Values{"upload_id": {token}}
			filesTestAuthDenied(t, filesTestAuthenticate(t, svc, changed), test.status)
		})
	}
	for _, raw := range []string{
		`{"caller_scope":"` + filesTestCaller + `","caller_scope":"` + filesTestCaller + `","file_id":"capability","expires_at":1790157600}`,
		`{"caller_scope":"` + filesTestCaller + `","file_id":"capability","expires_at":1790157600,"api_key":"forbidden"}`,
		`{"caller_scope":"` + filesTestCaller + `","file_id":"capability","expires_at":"1790157600"}`,
	} {
		token := filesTestSealClaims(t, svc, []byte(raw), filesUploadAAD)
		filesTestAuthDenied(t, filesTestAuthenticate(t, svc, filesUploadAuthRequest{Method: "POST", Path: filesResumablePath, Query: url.Values{"upload_id": {token}}}), 401)
	}
	for _, aad := range []string{"gemini-web-files-upload-capability-v2\x00POST\x00" + filesResumablePath, "gemini-web-files-upload-capability-v1\x00PUT\x00" + filesResumablePath, "gemini-web-files-upload-capability-v1\x00POST\x00/upload/v1beta/files"} {
		token := filesTestSealClaims(t, svc, jsonFixture(t, valid), aad)
		filesTestAuthDenied(t, filesTestAuthenticate(t, svc, filesUploadAuthRequest{Method: "POST", Path: filesResumablePath, Query: url.Values{"upload_id": {token}}}), 401)
	}
	unchanged()
}

func TestFilesUploadCapabilityFinalQueryExpiryAndHandlerIsolation(t *testing.T) {
	svc, fixture, request := filesTestCapability(t)
	server := filesTestHTTP(t, svc)
	location := server.URL + request.Path + "?" + request.RawQuery
	status, _, body := filesTestCommand(t, location, "files-caller-b", "upload, finalize", 0, []byte("abc"))
	if status != 200 {
		t.Fatalf("possessed capability upload failed: status=%d body=%s", status, body)
	}
	unchanged := filesTestUnchanged(t, svc, fixture)
	status, headers, body := filesTestCommand(t, location, "", "query", 0, nil)
	if status != 200 || headers.Get("X-Goog-Upload-Status") != "final" {
		t.Fatalf("anonymous final query failed: status=%d body=%s", status, body)
	}
	status, headers, _ = filesTestCommand(t, location, "", "upload, finalize", 0, []byte("xyz"))
	if status != 409 || headers.Get("X-Goog-Upload-Status") != "cancelled" {
		t.Fatal("finalized capability accepted new bytes")
	}
	for _, caller := range []string{"", strings.Repeat("b", 64)} {
		response, err := svc.filesHTTP(t.Context(), jsonFixture(t, filesHTTPRequest{Method: "POST", Path: request.Path, Query: request.Query, CallerScope: caller, Headers: http.Header{"X-Goog-Upload-Command": {"query"}}}))
		if err != nil || response.StatusCode != 401 || response.Headers.Get("X-Goog-Upload-Status") != "cancelled" {
			t.Fatal("handler trusted a foreign or absent caller scope")
		}
	}
	if _, err := svc.resolveFileSource(strings.Repeat("b", 64), "files/capability"); !filesMissing(err) {
		t.Fatal("capability use changed generation ownership")
	}
	expires := svc.now().Add(filesUploadLifetime)
	svc.now = func() time.Time { return expires.Add(-time.Second) }
	if auth := filesTestAuthenticate(t, svc, request); !auth.Authenticated {
		t.Fatal("capability expired early")
	}
	svc.now = func() time.Time { return expires }
	filesTestAuthDenied(t, filesTestAuthenticate(t, svc, request), 410)
	response, err := svc.filesHTTP(t.Context(), jsonFixture(t, filesHTTPRequest{Method: "POST", Path: request.Path, Query: request.Query, CallerScope: filesTestCaller, Headers: http.Header{"X-Goog-Upload-Command": {"query"}}}))
	if err != nil || response.StatusCode != 410 || response.Headers.Get("X-Goog-Upload-Status") != "cancelled" {
		t.Fatal("handler did not independently reject expiry")
	}
	unchanged()
}

func TestFilesUploadCapabilityRecordRevocation(t *testing.T) {
	for _, kind := range []string{"replaced", "deleting", "generated", "corrupt", "ciphertext"} {
		t.Run(kind, func(t *testing.T) {
			svc, fixture, request := filesTestCapability(t)
			if !filesTestAuthenticate(t, svc, request).Authenticated {
				t.Fatal("initial capability rejected")
			}
			store := svc.localStore()
			record, err := store.filesReadRecord(filesTestCaller, "capability")
			if err != nil {
				t.Fatal(err)
			}
			status := 404
			switch kind {
			case "replaced":
				store.mu.Lock()
				record.UploadID, err = store.filesNewUploadLocked(filesTestCaller, "capability", svc.now())
				store.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			case "deleting":
				record.File.State = "DELETING"
			case "generated":
				record.File.Source, record.GeneratedKey = "GENERATED", "generated-key"
				record.File.DownloadURI = "/v1beta/files/capability:download?alt=media"
			case "corrupt":
				record.File.URI = "files/other"
				status = 503
			}
			release, err := svc.fileLeases.acquire(t.Context(), filesMetadataName(filesTestCaller, "capability"))
			if err != nil {
				t.Fatal(err)
			}
			err = store.filesWriteRecord(filesTestCaller, record)
			release()
			if err != nil {
				t.Fatal(err)
			}
			if kind == "ciphertext" {
				path := filepath.Join(store.directory.Name(), filesMetadataName(filesTestCaller, "capability"))
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				raw[len(raw)-1] ^= 1
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				status = 503
			}
			unchanged := filesTestUnchanged(t, svc, fixture)
			filesTestAuthDenied(t, filesTestAuthenticate(t, svc, request), status)
			response, err := svc.filesHTTP(t.Context(), jsonFixture(t, filesHTTPRequest{Method: "POST", Path: request.Path, Query: request.Query, CallerScope: filesTestCaller, Headers: http.Header{"X-Goog-Upload-Command": {"query"}}}))
			if err != nil || response.StatusCode != status || response.Headers.Get("X-Goog-Upload-Status") != "cancelled" {
				t.Fatal("handler did not recheck revoked metadata after authentication")
			}
			unchanged()
		})
	}
}

func TestFilesUploadCapabilityCannotAuthenticateOrdinaryFilesRoutes(t *testing.T) {
	svc, fixture, request := filesTestCapability(t)
	unchanged := filesTestUnchanged(t, svc, fixture)
	server := filesTestHTTP(t, svc)
	for _, path := range []string{"/upload/v1beta/files", "/v1beta/files", "/v1beta/files/capability", "/v1beta/files/capability:download", "/v1beta/models/gemini-3.8-flash:generateContent"} {
		method := "GET"
		if strings.HasPrefix(path, "/upload/") || strings.HasPrefix(path, "/v1beta/models/") {
			method = "POST"
		}
		status, _, _ := filesTestRequest(t, method, server.URL+path+"?"+request.RawQuery, "", nil, nil)
		if status != 401 {
			t.Fatalf("capability replaced ordinary authentication at %s: %d", path, status)
		}
	}
	status, headers, _ := filesTestRequest(t, "POST", server.URL+"/upload/v1beta/files?"+request.RawQuery, "files-caller-a", filesStartHeaders(3), []byte(`{"file":{}}`))
	if status != 400 || headers.Get("X-Goog-Upload-Status") != "cancelled" {
		t.Fatal("ordinary start retained a legacy upload-session branch")
	}
	status, _, _ = filesTestRequest(t, "GET", server.URL+"/v1beta/files?pageToken="+request.Query.Get("upload_id"), "files-caller-a", nil, nil)
	if status != 400 {
		t.Fatal("upload capability was accepted as a page token")
	}
	unchanged()
}

func TestFilesUploadCapabilityDeletionReplacementAndKeyRotation(t *testing.T) {
	svc, fixture, request := filesTestCapability(t)
	if err := svc.filesDelete(context.Background(), svc.localStore(), filesTestCaller, "capability"); err != nil {
		t.Fatal(err)
	}
	filesTestAuthDenied(t, filesTestAuthenticate(t, svc, request), 404)
	server := filesTestHTTP(t, svc)
	location := filesTestStart(t, server.URL, "capability", 3)
	filesTestAuthDenied(t, filesTestAuthenticate(t, svc, request), 404)
	newURL, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	request.Query, request.RawQuery = newURL.Query(), newURL.RawQuery
	unchanged := filesTestUnchanged(t, svc, fixture)
	if !filesTestAuthenticate(t, svc, request).Authenticated {
		t.Fatal("replacement capability rejected")
	}
	unchanged()
	directory := svc.localStore().directory.Name()
	if err := svc.shutdownSessions(); err != nil {
		t.Fatal(err)
	}
	reopened := filesTestService(t, directory)
	reopened.now = svc.now
	fixture.attach(reopened)
	if !filesTestAuthenticate(t, reopened, request).Authenticated {
		t.Fatal("capability did not survive restart with its persistent key")
	}
	if err := reopened.shutdownSessions(); err != nil {
		t.Fatal(err)
	}
	store, err := openSessionStore(directory, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	rotated := newService(nil)
	rotated.sessions, rotated.now = store, svc.now
	fixture.attach(rotated)
	t.Cleanup(func() {
		if err := rotated.shutdownSessions(); err != nil {
			t.Error(err)
		}
	})
	unchanged = filesTestUnchanged(t, rotated, fixture)
	filesTestAuthDenied(t, filesTestAuthenticate(t, rotated, request), 401)
	unchanged()
}

// fileLeases.acquire calls Done after registering the lease waiter. This exact
// signal lets the test revoke authorization while the handler is blocked.
type filesLeaseWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (ctx *filesLeaseWaitContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

func TestFilesUploadCapabilityRecheckedAfterLeaseWait(t *testing.T) {
	for _, kind := range []string{"expiry", "deletion"} {
		t.Run(kind, func(t *testing.T) {
			svc, fixture, request := filesTestCapability(t)
			var clock atomic.Int64
			clock.Store(svc.now().Unix())
			svc.now = func() time.Time { return time.Unix(clock.Load(), 0) }
			store := svc.localStore()
			release, err := svc.fileLeases.acquire(t.Context(), filesMetadataName(filesTestCaller, "capability"))
			if err != nil {
				t.Fatal(err)
			}
			var releaseOnce sync.Once
			defer releaseOnce.Do(release)
			// The body-free verifier must not acquire the upload's operation lease.
			if !filesTestAuthenticate(t, svc, request).Authenticated {
				t.Fatal("capability rejected before revocation")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			waiting := &filesLeaseWaitContext{Context: ctx, waiting: make(chan struct{})}
			raw := jsonFixture(t, filesHTTPRequest{Method: "POST", Path: request.Path, Query: request.Query, CallerScope: filesTestCaller, Headers: http.Header{"X-Goog-Upload-Command": {"upload, finalize"}, "X-Goog-Upload-Offset": {"0"}}, Body: []byte("abc")})
			result := make(chan httpResponse, 1)
			go func() {
				response, err := svc.filesHTTP(waiting, raw)
				if err != nil {
					t.Error(err)
				}
				result <- response
			}()
			select {
			case <-waiting.waiting:
			case <-ctx.Done():
				t.Fatal("upload did not reach its file lease")
			}
			status := 410
			if kind == "expiry" {
				clock.Add(int64(filesUploadLifetime / time.Second))
			} else {
				record, err := store.filesReadRecord(filesTestCaller, "capability")
				if err != nil {
					t.Fatal(err)
				}
				record.File.State = "DELETING"
				if err := store.filesWriteRecord(filesTestCaller, record); err != nil {
					t.Fatal(err)
				}
				status = 404
			}
			unchanged := filesTestUnchanged(t, svc, fixture)
			releaseOnce.Do(release)
			select {
			case response := <-result:
				if response.StatusCode != status || response.Headers.Get("X-Goog-Upload-Status") != "cancelled" {
					t.Fatal("upload used stale authentication after waiting for its lease")
				}
			case <-ctx.Done():
				t.Fatal("revoked upload did not finish")
			}
			unchanged()
		})
	}
}
