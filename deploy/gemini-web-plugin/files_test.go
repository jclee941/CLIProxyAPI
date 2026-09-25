package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const filesTestCaller = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func filesTestService(t *testing.T, directory string) *service {
	t.Helper()
	store, err := openSessionStore(directory, sessionKeyFixture())
	if err != nil {
		t.Fatal(err)
	}
	svc := newService(nil)
	svc.sessions = store
	svc.config.NativeGeneration = true
	t.Cleanup(func() {
		if err := svc.shutdownSessions(); err != nil {
			t.Error(err)
		}
	})
	return svc
}

// This plugin protocol adapter exercises JSON RPC, NOT core authentication.
// Only the resumable route uses the real capability verifier; other routes use
// fixed fixture identities. Native/core integration is verified separately.
func filesTestHTTP(t *testing.T, svc *service) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := ""
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if key == "" {
			key = r.Header.Get("X-Goog-Api-Key")
		}
		switch key {
		case "files-caller-a":
			caller = filesTestCaller
		case "files-caller-b":
			caller = strings.Repeat("b", 64)
		}
		if r.URL.Path == filesResumablePath {
			auth := filesTestAuthenticate(t, svc, filesUploadAuthRequest{Method: r.Method, Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: r.URL.RawQuery, Query: r.URL.Query()})
			if !auth.Authenticated {
				for name, values := range auth.Rejection.Headers {
					w.Header()[name] = values
				}
				w.WriteHeader(auth.Rejection.StatusCode)
				if _, err := w.Write(auth.Rejection.Body); err != nil {
					t.Error(err)
				}
				return
			}
			caller = auth.CallerScope
		}
		if caller == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		raw := jsonFixture(t, map[string]any{
			"Method": r.Method, "Path": r.URL.Path, "Scheme": "http", "Host": r.Host,
			"RawPath": r.URL.RawPath, "RawQuery": r.URL.RawQuery,
			"Headers": r.Header, "Query": r.URL.Query(), "Body": body,
			"caller_scope": caller,
		})
		var reply envelope
		if err := json.Unmarshal(svc.handle(r.Context(), "frontend_http.handle", raw), &reply); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		if !reply.OK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(reply.Error.HTTPStatus)
			if err := json.NewEncoder(w).Encode(map[string]any{"error": reply.Error}); err != nil {
				t.Error(err)
			}
			return
		}
		var response httpResponse
		if err := json.Unmarshal(reply.Result, &response); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		for name, values := range response.Headers {
			w.Header()[name] = values
		}
		w.WriteHeader(response.StatusCode)
		if _, err := w.Write(response.Body); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func filesTestRequest(t *testing.T, method, location, caller string, headers http.Header, body []byte) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, location, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if headers != nil {
		req.Header = headers.Clone()
	}
	if caller != "" {
		req.Header.Set("Authorization", "Bearer "+caller)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	if closeErr := response.Body.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, response.Header, data
}

func filesTestStart(t *testing.T, server, name string, size int) string {
	t.Helper()
	body := jsonFixture(t, map[string]any{"file": map[string]any{"name": "files/" + name, "display_name": "private display name", "mime_type": "video/mp4", "size_bytes": size}})
	status, headers, raw := filesTestRequest(t, "POST", server+"/upload/v1beta/files", "files-caller-a", http.Header{
		"Content-Type":                        {"application/json"},
		"X-Goog-Upload-Protocol":              {"resumable"},
		"X-Goog-Upload-Command":               {"start"},
		"X-Goog-Upload-Header-Content-Length": {strconv.Itoa(size)},
		"X-Goog-Upload-Header-Content-Type":   {"video/mp4"},
	}, body)
	if status != 200 || headers.Get("X-Goog-Upload-URL") == "" {
		t.Fatalf("start status=%d headers=%v body=%s", status, headers, raw)
	}
	location := headers.Get("X-Goog-Upload-URL")
	if !strings.HasPrefix(location, server+"/") || strings.Contains(location, "files-caller-a") || strings.Contains(location, filesTestCaller) {
		t.Fatalf("upload URL is not local and opaque: %s", location)
	}
	return location
}

func filesTestCommand(t *testing.T, location, caller, command string, offset int, body []byte) (int, http.Header, []byte) {
	t.Helper()
	return filesTestRequest(t, "POST", location, caller, http.Header{
		"X-Goog-Upload-Command": {command}, "X-Goog-Upload-Offset": {strconv.Itoa(offset)},
	}, body)
}

func TestFilesHTTPResumableContract(t *testing.T) {
	svc := filesTestService(t, filepath.Join(t.TempDir(), "sessions"))
	newFilesDriveFixture(t).attach(svc)
	server := filesTestHTTP(t, svc)
	content := append(bytes.Repeat([]byte{'v'}, filesGranularity), []byte("private-video-content")...)
	location := filesTestStart(t, server.URL, "contract-file", len(content))
	status, headers, raw := filesTestCommand(t, location, "files-caller-a", "upload", 0, content[:filesGranularity])
	if status != 200 || headers.Get("X-Goog-Upload-Status") != "active" || headers.Get("X-Goog-Upload-Size-Received") != strconv.Itoa(filesGranularity) {
		t.Fatalf("chunk status=%d headers=%v body=%s", status, headers, raw)
	}
	status, headers, raw = filesTestCommand(t, location, "files-caller-a", "query", 0, nil)
	if status != 200 || headers.Get("X-Goog-Upload-Size-Received") != strconv.Itoa(filesGranularity) {
		t.Fatalf("query status=%d headers=%v body=%s", status, headers, raw)
	}
	status, _, _ = filesTestCommand(t, location, "files-caller-a", "upload", filesGranularity+1, []byte("bad"))
	if status != 400 {
		t.Fatalf("wrong offset accepted: %d", status)
	}
	status, headers, raw = filesTestCommand(t, location, "files-caller-a", "upload, finalize", filesGranularity, content[filesGranularity:])
	if status != 200 || headers.Get("X-Goog-Upload-Status") != "final" {
		t.Fatalf("finalize status=%d headers=%v body=%s", status, headers, raw)
	}
	var finalized struct {
		File map[string]any `json:"file"`
	}
	if err := json.Unmarshal(raw, &finalized); err != nil {
		t.Fatal(err)
	}
	file := finalized.File
	if file["name"] != "files/contract-file" || file["state"] != "ACTIVE" || file["source"] != "UPLOADED" || file["sizeBytes"] != strconv.Itoa(len(content)) || file["downloadUri"] != nil {
		t.Fatalf("invalid uploaded file: %s", raw)
	}
	status, _, raw = filesTestRequest(t, "GET", server.URL+"/v1beta/files/contract-file", "files-caller-a", nil, nil)
	if status != 200 || !bytes.Contains(raw, []byte(`"state":"ACTIVE"`)) {
		t.Fatalf("get status=%d body=%s", status, raw)
	}
	status, _, _ = filesTestRequest(t, "GET", server.URL+"/v1beta/files/contract-file:download?alt=media", "files-caller-a", nil, nil)
	if status != 400 {
		t.Fatalf("uploaded bytes were downloadable: %d", status)
	}
	status, _, raw = filesTestRequest(t, "GET", server.URL+"/v1beta/files", "files-caller-a", nil, nil)
	if status != 200 || !bytes.Contains(raw, []byte(`"name":"files/contract-file"`)) {
		t.Fatalf("list status=%d body=%s", status, raw)
	}
	status, _, raw = filesTestRequest(t, "DELETE", server.URL+"/v1beta/files/contract-file", "files-caller-a", nil, nil)
	if status != 200 || string(raw) != "{}" {
		t.Fatalf("delete status=%d body=%s", status, raw)
	}
	status, _, _ = filesTestRequest(t, "GET", server.URL+"/v1beta/files/contract-file", "files-caller-a", nil, nil)
	if status != 404 {
		t.Fatalf("deleted file visible: %d", status)
	}
}

func TestFilesStoreReloadCallerIsolation(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "sessions")
	fixture := newFilesDriveFixture(t)
	first := filesTestService(t, directory)
	fixture.attach(first)
	server := filesTestHTTP(t, first)
	location := filesTestStart(t, server.URL, "reload", filesGranularity+3)
	status, _, raw := filesTestCommand(t, location, "files-caller-a", "upload", 0, bytes.Repeat([]byte{'a'}, filesGranularity))
	if status != 200 {
		t.Fatalf("upload status=%d body=%s", status, raw)
	}
	if err := first.shutdownSessions(); err != nil {
		t.Fatal(err)
	}
	second := filesTestService(t, directory)
	fixture.attach(second)
	reopened := filesTestHTTP(t, second)
	location = reopened.URL + strings.TrimPrefix(location, server.URL)
	status, headers, raw := filesTestCommand(t, location, "", "query", 0, nil)
	if status != 200 || headers.Get("X-Goog-Upload-Size-Received") != strconv.Itoa(filesGranularity) {
		t.Fatalf("reloaded query status=%d headers=%v body=%s", status, headers, raw)
	}
	status, _, _ = filesTestCommand(t, location, "files-caller-b", "query", 0, nil)
	if status != 200 {
		t.Fatalf("possessed capability did not retain its original owner: %d", status)
	}
	status, _, raw = filesTestCommand(t, location, "files-caller-a", "upload, finalize", filesGranularity, []byte("def"))
	if status != 200 {
		t.Fatalf("resumed upload status=%d body=%s", status, raw)
	}
	for _, method := range []string{"GET", "DELETE"} {
		status, _, _ := filesTestRequest(t, method, reopened.URL+"/v1beta/files/reload", "files-caller-b", http.Header{"Caller-Scope": {filesTestCaller}}, nil)
		if status != 404 {
			t.Fatalf("%s caller isolation status=%d", method, status)
		}
	}
	status, _, raw = filesTestRequest(t, "GET", reopened.URL+"/v1beta/files", "files-caller-b", nil, nil)
	if status != 200 || string(raw) != `{"files":[]}` {
		t.Fatalf("foreign list status=%d body=%s", status, raw)
	}
	status, _, _ = filesTestRequest(t, "GET", reopened.URL+"/v1beta/files/reload", "", nil, nil)
	if status != 401 {
		t.Fatalf("anonymous request accepted: %d", status)
	}
	// Wrong-key initialization must never turn encrypted files into an empty list.
	if err := second.shutdownSessions(); err != nil {
		t.Fatal(err)
	}
	wrong, err := openSessionStore(directory, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)))
	if err == nil {
		t.Cleanup(func() {
			if err := wrong.close(); err != nil {
				t.Error(err)
			}
		})
		// Existing Files metadata is validated when read, not by the account
		// scanner, so the first authenticated operation must reject the wrong key.
		bad := newService(nil)
		bad.sessions = wrong
		badServer := filesTestHTTP(t, bad)
		status, _, _ := filesTestRequest(t, "GET", badServer.URL+"/v1beta/files/reload", "files-caller-a", nil, nil)
		if status != 503 {
			t.Fatalf("wrong key status=%d", status)
		}
	}
}
