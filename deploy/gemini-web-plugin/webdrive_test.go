package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
)

func TestDriveFileIDReadsTheLinkFormsACallerHas(t *testing.T) {
	for reference, want := range map[string]string{
		"drive:1A2B3C4D5E6F7G8H":                                "1A2B3C4D5E6F7G8H",
		"https://drive.google.com/file/d/1A2B3C4D5E6F7G8H/view": "1A2B3C4D5E6F7G8H",
		"https://drive.google.com/open?id=1A2B3C4D5E6F7G8H":     "1A2B3C4D5E6F7G8H",
		"https://drive.google.com/uc?id=1A2B3C4D5E6F7G8H":       "1A2B3C4D5E6F7G8H",
	} {
		identifier, ok := driveFileID(reference)
		if !ok || identifier != want {
			t.Fatalf("%s resolved to %q (ok=%v), want %q", reference, identifier, ok, want)
		}
	}
	for _, reference := range []string{
		"https://example.invalid/file/d/1A2B3C4D5E6F7G8H/view",
		"files/abc-123",
		"drive:short",
		"",
	} {
		if identifier, ok := driveFileID(reference); ok {
			t.Fatalf("%q was accepted as a Drive file: %s", reference, identifier)
		}
	}
}

type driveFixture struct {
	server *httptest.Server
	guard  sync.Mutex
	media  int
	auth   string
}

func (fixture *driveFixture) mediaCalls() int {
	fixture.guard.Lock()
	defer fixture.guard.Unlock()
	return fixture.media
}

func (fixture *driveFixture) authorization() string {
	fixture.guard.Lock()
	defer fixture.guard.Unlock()
	return fixture.auth
}

func newDriveFixture(t *testing.T, content string, status int) *driveFixture {
	t.Helper()
	fixture := &driveFixture{}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.guard.Lock()
		fixture.auth = request.Header.Get("Authorization")
		if request.URL.Query().Get("alt") == "media" {
			fixture.media++
		}
		fixture.guard.Unlock()
		if status != http.StatusOK {
			writer.WriteHeader(status)
			return
		}
		body := `{"mimeType":"image/png","size":"` + strconv.Itoa(len(content)) + `"}`
		if request.URL.Query().Get("alt") == "media" {
			body = content
		}
		if _, err := writer.Write([]byte(body)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func driveService(t *testing.T, fixture *driveFixture) *service {
	t.Helper()
	t.Setenv("GOOGLE_DRIVE_API_KEY", "test-key")
	t.Setenv("GOOGLE_DRIVE_CLIENT_ID", "")
	t.Setenv("GOOGLE_DRIVE_CLIENT_SECRET", "")
	t.Setenv("GOOGLE_DRIVE_REFRESH_TOKEN", "")
	service := newService(nil)
	if fixture != nil {
		service.driveOverride = fixture.server.URL
	}
	return service
}

// The file must not travel until the upload is ready for it, because holding it
// here is exactly what the attachment bound used to exist to prevent.
func TestDriveSourceTakesTypeAndLengthBeforeAnyBytesMove(t *testing.T) {
	fixture := newDriveFixture(t, "png-bytes", http.StatusOK)
	service := driveService(t, fixture)

	source, err := service.driveSource(context.Background(), "drive:1A2B3C4D5E6F7G8H")

	if err != nil {
		t.Fatalf("a shared file was not described: %v", err)
	}
	if source.MIMEType != "image/png" || source.Size != int64(len("png-bytes")) {
		t.Fatalf("metadata did not reach the source: %+v", source)
	}
	if calls := fixture.mediaCalls(); calls != 0 {
		t.Fatalf("the file was fetched %d times before the upload asked for it", calls)
	}
	body, err := source.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(body)
	if err != nil || string(content) != "png-bytes" {
		t.Fatalf("the bytes did not survive the stream: %q (%v)", content, err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if calls := fixture.mediaCalls(); calls != 1 {
		t.Fatalf("the file was fetched %d times, want once", calls)
	}
}

// An API key cannot see a private file and Drive reports that as absence, so the
// error has to say the file is not shared rather than that it does not exist.
func TestDriveSourceNamesAnUnsharedFile(t *testing.T) {
	service := driveService(t, newDriveFixture(t, "", http.StatusNotFound))

	_, err := service.driveSource(context.Background(), "drive:1A2B3C4D5E6F7G8H")

	var public *publicError
	if !errors.As(err, &public) || public.Code != "drive_file_not_shared" {
		t.Fatalf("an unshared file was not named as such: %v", err)
	}
}

// A folder and a Google Doc report no length, and an upload has to declare one
// before it may send anything.
func TestDriveSourceRefusesAFileWithNoLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if _, err := writer.Write([]byte(`{"mimeType":"application/vnd.google-apps.folder"}`)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	service := driveService(t, nil)
	service.driveOverride = server.URL

	_, err := service.driveSource(context.Background(), "drive:1A2B3C4D5E6F7G8H")

	var public *publicError
	if !errors.As(err, &public) || public.Code != "drive_file_not_downloadable" {
		t.Fatalf("a file with no length was accepted: %v", err)
	}
}

func TestDriveSourceRefusesBeforeCallingWithoutCredentials(t *testing.T) {
	service := driveService(t, nil)
	t.Setenv("GOOGLE_DRIVE_API_KEY", "")

	if _, err := service.driveSource(context.Background(), "drive:1A2B3C4D5E6F7G8H"); err == nil {
		t.Fatal("a fetch was attempted without a credential")
	}
}

// An API key reaches only link-shared files, so when an OAuth client is
// configured the request has to carry the token instead, and one token has to
// serve every attachment until it expires.
func TestDriveCarriesTheOAuthTokenAndReusesIt(t *testing.T) {
	fixture := newDriveFixture(t, "png-bytes", http.StatusOK)
	var guard sync.Mutex
	exchanges := 0
	tokens := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		guard.Lock()
		exchanges++
		guard.Unlock()
		if _, err := writer.Write([]byte(`{"access_token":"at-1","expires_in":3600}`)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(tokens.Close)
	t.Setenv("GOOGLE_DRIVE_API_KEY", "")
	t.Setenv("GOOGLE_DRIVE_CLIENT_ID", "cid")
	t.Setenv("GOOGLE_DRIVE_CLIENT_SECRET", "secret")
	t.Setenv("GOOGLE_DRIVE_REFRESH_TOKEN", "refresh")
	service := newService(nil)
	service.driveOverride, service.driveTokenOverride = fixture.server.URL, tokens.URL

	for range 2 {
		if _, err := service.driveSource(context.Background(), "drive:1A2B3C4D5E6F7G8H"); err != nil {
			t.Fatal(err)
		}
	}

	if authorization := fixture.authorization(); authorization != "Bearer at-1" {
		t.Fatalf("the request did not carry the token: %q", authorization)
	}
	guard.Lock()
	defer guard.Unlock()
	if exchanges != 1 {
		t.Fatalf("the token was exchanged %d times, want it reused once", exchanges)
	}
}

func TestMediaSourcesLeaveInlineAttachmentsAlone(t *testing.T) {
	fixture := newDriveFixture(t, "png-bytes", http.StatusOK)
	service := driveService(t, fixture)

	sources, err := service.mediaSources(context.Background(), []webMedia{
		{MIMEType: "image/jpeg", Data: "AAAA"},
		{Reference: "drive:1A2B3C4D5E6F7G8H"},
	}, "")

	if err != nil {
		t.Fatal(err)
	}
	if sources[0].MIMEType != "image/jpeg" || sources[0].Size != 3 {
		t.Fatalf("an inline attachment was rewritten: %+v", sources[0])
	}
	if sources[1].MIMEType != "image/png" {
		t.Fatalf("the reference was not described from Drive: %+v", sources[1])
	}
	if calls := fixture.mediaCalls(); calls != 0 {
		t.Fatalf("describing the attachments already moved %d files", calls)
	}
}
