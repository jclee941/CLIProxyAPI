package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type filesDriveObject struct {
	ID, MIMEType string
	Size         int64
	Data         []byte
	Started      bool
	Complete     bool
}

// A stateful HTTP Drive fixture enforces preallocated IDs, private OAuth-only
// writes, Content-Range offsets and real Drive nonfinal chunk granularity.
type filesDriveFixture struct {
	server      *httptest.Server
	mu          sync.Mutex
	objects     map[string]*filesDriveObject
	allocated   int
	requests    int
	writes      int
	media       int
	loseNextPUT bool
	denyWrites  bool
	readOnly    bool
	failDelete  bool
	badLocation bool
}

func newFilesDriveFixture(t *testing.T) *filesDriveFixture {
	t.Helper()
	fixture := &filesDriveFixture{objects: make(map[string]*filesDriveObject)}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		fixture.requests++
		if r.URL.Path == "/token" {
			if err := r.ParseForm(); err != nil || r.Form.Get("client_id") != "files-test-client" || r.Form.Get("client_secret") != "files-test-secret" || r.Form.Get("refresh_token") != "files-test-refresh" || r.Form.Get("grant_type") != "refresh_token" {
				t.Error("Drive OAuth refresh contract changed")
				w.WriteHeader(400)
				return
			}
			scope := "https://www.googleapis.com/auth/drive.file"
			if fixture.readOnly {
				scope = "https://www.googleapis.com/auth/drive.readonly"
			}
			filesFixtureJSON(t, w, map[string]any{"access_token": "files-test-drive-bearer", "expires_in": 3600, "scope": scope})
			return
		}
		if r.Header.Get("Authorization") != "Bearer files-test-drive-bearer" || r.URL.Query().Get("key") != "" {
			t.Error("Drive request did not use private OAuth authentication")
			w.WriteHeader(401)
			return
		}
		if fixture.denyWrites && r.URL.Query().Get("alt") != "media" {
			w.WriteHeader(403)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/drive/v3/files/generateIds" {
			fixture.allocated++
			id := fmt.Sprintf("privateDrive%020d", fixture.allocated)
			fixture.objects[id] = &filesDriveObject{ID: id}
			filesFixtureJSON(t, w, map[string]any{"ids": []string{id}})
			return
		}
		if r.Method == "POST" && r.URL.Path == "/upload/drive/v3/files" {
			var metadata struct{ ID, Name, MIMEType string }
			if json.NewDecoder(r.Body).Decode(&metadata) != nil || fixture.objects[metadata.ID] == nil || !strings.HasPrefix(metadata.Name, "cpa-native-") || len(metadata.Name) != len("cpa-native-")+32 {
				t.Error("Drive create was not tied to a private preallocated ID")
				w.WriteHeader(400)
				return
			}
			size, err := strconv.ParseInt(r.Header.Get("X-Upload-Content-Length"), 10, 64)
			if err != nil || r.Header.Get("X-Upload-Content-Type") != metadata.MIMEType || r.URL.Query().Get("uploadType") != "resumable" {
				t.Error("Drive resumable start headers changed")
				w.WriteHeader(400)
				return
			}
			object := fixture.objects[metadata.ID]
			object.Size, object.MIMEType, object.Started = size, metadata.MIMEType, true
			fixture.writes++
			location := fixture.server.URL + "/upload/drive/v3/files?uploadType=resumable&upload_id=" + metadata.ID
			if fixture.badLocation {
				location = "https://untrusted.invalid/upload/drive/v3/files?uploadType=resumable&upload_id=private"
			}
			w.Header().Set("Location", location)
			return
		}
		if r.Method == "PUT" && r.URL.Path == "/upload/drive/v3/files" {
			object := fixture.objects[r.URL.Query().Get("upload_id")]
			if object == nil || !object.Started {
				w.WriteHeader(404)
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			query := r.Header.Get("Content-Range") == "bytes */"+strconv.FormatInt(object.Size, 10)
			if !query {
				var start, end, total int64
				_, err := fmt.Sscanf(r.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total)
				if err != nil || start != int64(len(object.Data)) || total != object.Size || end-start+1 != int64(len(body)) || end >= total || end+1 < total && len(body)%filesGranularity != 0 {
					t.Error("Drive chunk offset, length, total or granularity invalid")
					w.WriteHeader(400)
					return
				}
				object.Data = append(object.Data, body...)
				fixture.writes++
			} else if len(body) != 0 {
				t.Error("Drive offset query carried bytes")
				w.WriteHeader(400)
				return
			}
			object.Complete = int64(len(object.Data)) == object.Size
			if !query && fixture.loseNextPUT {
				fixture.loseNextPUT = false
				w.WriteHeader(500)
				return
			}
			if object.Complete {
				digest := sha256.Sum256(object.Data)
				filesFixtureJSON(t, w, map[string]string{"id": object.ID, "size": strconv.FormatInt(object.Size, 10), "mimeType": object.MIMEType, "sha256Checksum": hex.EncodeToString(digest[:])})
				return
			}
			if len(object.Data) != 0 {
				w.Header().Set("Range", "bytes=0-"+strconv.Itoa(len(object.Data)-1))
			}
			w.WriteHeader(308)
			return
		}
		if id, valid := strings.CutPrefix(r.URL.Path, "/drive/v3/files/"); valid {
			object := fixture.objects[id]
			if r.Method == "DELETE" {
				if fixture.failDelete {
					fixture.failDelete = false
					w.WriteHeader(503)
					return
				}
				delete(fixture.objects, id)
				fixture.writes++
				w.WriteHeader(204)
				return
			}
			if r.Method == "GET" && r.URL.Query().Get("alt") == "media" && object != nil && object.Complete {
				fixture.media++
				w.Header().Set("Content-Type", object.MIMEType)
				w.Header().Set("Content-Length", strconv.Itoa(len(object.Data)))
				if _, err := w.Write(object.Data); err != nil {
					t.Error(err)
				}
				return
			}
		}
		t.Errorf("unexpected Drive request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func filesFixtureJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}

func (fixture *filesDriveFixture) attach(svc *service) {
	svc.config.DriveClientID = "files-test-client"
	svc.config.DriveClientSecret = "files-test-secret"
	svc.config.DriveRefreshToken = "files-test-refresh"
	svc.driveOverride = fixture.server.URL
	svc.driveTokenOverride = fixture.server.URL + "/token"
}

func TestFilesGeneratedDrivePersistenceAndPrivateDownload(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "sessions")
	fixture := newFilesDriveFixture(t)
	svc := filesTestService(t, directory)
	fixture.attach(svc)
	content := []byte("private generated bytes stored only on Drive")
	file, err := svc.saveGeneratedFile(context.Background(), filesTestCaller, "receipt-output-0", "video/mp4", content)
	if err != nil {
		t.Fatal(err)
	}
	same, err := svc.saveGeneratedFile(context.Background(), filesTestCaller, "receipt-output-0", "video/mp4", content)
	if err != nil || same != file {
		t.Fatalf("generated mapping not idempotent: %v", err)
	}
	if file.Source != "GENERATED" || file.DownloadURI == "" || file.URI != file.Name || strings.Contains(file.URI, "Drive") {
		t.Fatalf("generated metadata invalid: %+v", file)
	}
	if _, err := svc.saveGeneratedFile(context.Background(), filesTestCaller, "receipt-output-0", "video/mp4", []byte("different bytes")); err == nil {
		t.Fatal("a generated key changed its bytes")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("unsafe metadata permissions")
		}
		if entry.Name() != "owner.lock" && (!strings.HasSuffix(entry.Name(), ".meta") || info.Size() > 4096) {
			t.Fatalf("file bytes were duplicated locally: %s (%d bytes)", entry.Name(), info.Size())
		}
		raw, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, private := range [][]byte{content, []byte("privateDrive"), []byte(file.Name), []byte(filesTestCaller), []byte(fixture.server.URL), []byte("receipt-output-0")} {
			if bytes.Contains(raw, private) {
				t.Fatal("plaintext Files metadata or bytes persisted locally")
			}
		}
	}
	if err := svc.shutdownSessions(); err != nil {
		t.Fatal(err)
	}
	reloaded := filesTestService(t, directory)
	fixture.attach(reloaded)
	server := filesTestHTTP(t, reloaded)
	status, headers, body := filesTestRequest(t, "GET", server.URL+file.DownloadURI, "files-caller-a", nil, nil)
	if status != 200 || headers.Get("Content-Type") != "video/mp4" || !bytes.Equal(body, content) {
		t.Fatalf("generated download status=%d body=%q", status, body)
	}
	fixture.mu.Lock()
	requests := fixture.requests
	fixture.mu.Unlock()
	status, _, _ = filesTestRequest(t, "GET", server.URL+file.DownloadURI, "files-caller-b", nil, nil)
	if status != 404 {
		t.Fatalf("foreign generated download status=%d", status)
	}
	fixture.mu.Lock()
	if fixture.requests != requests {
		t.Error("foreign caller reached Drive")
	}
	fixture.mu.Unlock()
	source, err := reloaded.resolveFileSource(filesTestCaller, file.URI)
	if err != nil || source.Size != int64(len(content)) || source.MIMEType != "video/mp4" {
		t.Fatalf("generated source metadata: %v", err)
	}
	reader, err := source.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	if closeErr := reader.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("source bytes changed: %v", err)
	}
	for _, reference := range []string{"https://example.invalid/private", "drive:privateDrive00000000000000000001", "/v1beta/" + file.Name} {
		if _, err := reloaded.resolveFileSource(filesTestCaller, reference); err == nil {
			t.Fatalf("non-Files reference accepted: %q", reference)
		}
	}
	if _, err := reloaded.resolveFileSource(strings.Repeat("b", 64), file.URI); !filesMissing(err) {
		t.Fatalf("source caller binding failed: %v", err)
	}
}

func TestFilesDriveUnknownUploadOutcomeRecoveredByQuery(t *testing.T) {
	fixture := newFilesDriveFixture(t)
	svc := filesTestService(t, filepath.Join(t.TempDir(), "sessions"))
	fixture.attach(svc)
	server := filesTestHTTP(t, svc)
	location := filesTestStart(t, server.URL, "uncertain", 8)
	fixture.mu.Lock()
	fixture.loseNextPUT = true
	fixture.mu.Unlock()
	status, headers, _ := filesTestCommand(t, location, "files-caller-a", "upload, finalize", 0, []byte("contents"))
	if status != 502 || headers.Get("X-Goog-Upload-Status") != "cancelled" {
		t.Fatalf("ambiguous upload reported success or prompted SDK retry: %d", status)
	}
	status, headers, body := filesTestCommand(t, location, "files-caller-a", "query", 0, nil)
	if status != 200 || headers.Get("X-Goog-Upload-Status") != "final" || headers.Get("X-Goog-Upload-Size-Received") != "8" || !bytes.Contains(body, []byte(`"state":"ACTIVE"`)) {
		t.Fatalf("Drive outcome not reconciled: status=%d body=%s", status, body)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.allocated != 1 || fixture.writes != 2 {
		t.Fatal("recovery duplicated Drive create or bytes")
	}
}
