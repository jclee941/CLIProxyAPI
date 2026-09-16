package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
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

func driveServer(t *testing.T, content []byte, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if status != http.StatusOK {
			writer.WriteHeader(status)
			return
		}
		body := []byte(`{"mimeType":"image/png"}`)
		if request.URL.Query().Get("alt") == "media" {
			body = content
		}
		if _, err := writer.Write(body); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func driveService(t *testing.T, server *httptest.Server) *service {
	t.Helper()
	t.Setenv("GOOGLE_DRIVE_API_KEY", "test-key")
	service := newService(nil)
	if server != nil {
		service.driveOverride = server.URL
	}
	return service
}

func TestDriveFetchCarriesTheSharedBytesAndTheirType(t *testing.T) {
	service := driveService(t, driveServer(t, []byte("png-bytes"), http.StatusOK))

	media, err := service.fetchDrive(context.Background(), "drive:1A2B3C4D5E6F7G8H")

	if err != nil {
		t.Fatalf("a shared file was not fetched: %v", err)
	}
	if media.MIMEType != "image/png" {
		t.Fatalf("the media type did not come from Drive: %s", media.MIMEType)
	}
	decoded, err := base64.StdEncoding.DecodeString(media.Data)
	if err != nil || string(decoded) != "png-bytes" {
		t.Fatalf("the bytes did not survive the fetch: %q (%v)", decoded, err)
	}
}

// An API key cannot see a private file and Drive reports that as absence, so the
// error has to say the file is not shared rather than that it does not exist.
func TestDriveFetchNamesAnUnsharedFile(t *testing.T) {
	service := driveService(t, driveServer(t, nil, http.StatusNotFound))

	_, err := service.fetchDrive(context.Background(), "drive:1A2B3C4D5E6F7G8H")

	var public *publicError
	if !errors.As(err, &public) || public.Code != "drive_file_not_shared" {
		t.Fatalf("an unshared file was not named as such: %v", err)
	}
}

func TestDriveFetchRefusesBeforeCallingWithoutAKey(t *testing.T) {
	service := driveService(t, nil)
	t.Setenv("GOOGLE_DRIVE_API_KEY", "")

	if _, err := service.fetchDrive(context.Background(), "drive:1A2B3C4D5E6F7G8H"); err == nil {
		t.Fatal("a fetch was attempted without a key")
	}
}

// An API key reaches only link-shared files, so when an OAuth client is
// configured the request has to carry the token instead, and one token has to
// serve every attachment until it expires.
func TestDriveCarriesTheOAuthTokenAndReusesIt(t *testing.T) {
	var guard sync.Mutex
	authorization, exchanges := "", 0
	drive := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		guard.Lock()
		authorization = request.Header.Get("Authorization")
		guard.Unlock()
		if request.URL.Query().Get("key") != "" {
			t.Errorf("the api key travelled beside the token: %s", request.URL.RawQuery)
		}
		body := []byte(`{"mimeType":"application/pdf"}`)
		if request.URL.Query().Get("alt") == "media" {
			body = []byte("pdf-bytes")
		}
		if _, err := writer.Write(body); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(drive.Close)
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
	service.driveOverride, service.driveTokenOverride = drive.URL, tokens.URL

	for range 2 {
		if _, err := service.fetchDrive(context.Background(), "drive:1A2B3C4D5E6F7G8H"); err != nil {
			t.Fatal(err)
		}
	}

	guard.Lock()
	defer guard.Unlock()
	if authorization != "Bearer at-1" {
		t.Fatalf("the request did not carry the token: %q", authorization)
	}
	if exchanges != 1 {
		t.Fatalf("the token was exchanged %d times, want it reused once", exchanges)
	}
}

func TestResolveMediaLeavesInlineAttachmentsAlone(t *testing.T) {
	service := driveService(t, driveServer(t, []byte("png-bytes"), http.StatusOK))

	resolved, err := service.resolveMedia(context.Background(), []webMedia{
		{MIMEType: "image/jpeg", Data: "AAAA"},
		{Reference: "drive:1A2B3C4D5E6F7G8H"},
	})

	if err != nil {
		t.Fatal(err)
	}
	if resolved[0].MIMEType != "image/jpeg" || resolved[0].Data != "AAAA" {
		t.Fatalf("an inline attachment was rewritten: %+v", resolved[0])
	}
	if resolved[1].Reference != "" || resolved[1].MIMEType != "image/png" {
		t.Fatalf("the reference was not resolved into bytes: %+v", resolved[1])
	}
}
