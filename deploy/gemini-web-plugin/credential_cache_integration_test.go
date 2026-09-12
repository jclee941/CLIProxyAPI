package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestFlashReusesCredential_whenVaultBecomesUnavailable(t *testing.T) {
	// Given a real CLI boundary and one successfully authenticated request.
	directory := t.TempDir()
	command := "#!/bin/sh\n" +
		"printf x >> \"$CACHE_TEST_DIRECTORY/reads\"\n" +
		"test ! -f \"$CACHE_TEST_DIRECTORY/unavailable\" || exit 1\n" +
		"test \"$1\" = read || exit 2\n" +
		"printf %s \"$CACHE_TEST_TOKEN\"\n"
	if err := os.WriteFile(filepath.Join(directory, "op"), []byte(command), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CACHE_TEST_DIRECTORY", directory)
	t.Setenv("CACHE_TEST_TOKEN", encodedToken("SID=synthetic-cache"))
	service := newService(nil)
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("x-goog-api-key") != encodedToken("SID=synthetic-cache") {
			t.Error("selected credential changed")
		}
		switch request.URL.Path {
		case "/v1/account-models":
			writeFixture(t, writer, `{"available":true,"models":[{"capability_id":"flash","display_name":"3.8 Flash","mode":1}]}`)
		case "/v1beta/models/gemini-3.8-flash:generateContent":
			writeFixture(t, writer, `{"candidates":[{"content":{"parts":[{"text":"cache response"}]}}]}`)
		default:
			t.Errorf("unexpected path %s", request.URL.Path)
		}
	})
	record := recordFixture(t, "a")
	request := executorRequest{AuthID: record.ID, AuthProvider: provider, Model: flashModel, Format: "gemini", SourceFormat: "gemini", StorageJSON: jsonFixture(t, record), Payload: []byte(`{"contents":[{"parts":[{"text":"test"}]}]}`)}
	if result := invoke(t, service, "executor.execute", request); !result.OK {
		t.Fatalf("initial request failed: %+v", result.Error)
	}
	if err := os.WriteFile(filepath.Join(directory, "unavailable"), nil, 0600); err != nil {
		t.Fatal(err)
	}

	// When the same account is used while the vault is unavailable.
	result := invoke(t, service, "executor.execute", request)

	// Then inference continues without another credential-store request.
	if !result.OK {
		t.Fatalf("warm request depended on the unavailable vault: %+v", result.Error)
	}
	reads, err := os.ReadFile(filepath.Join(directory, "reads"))
	if err != nil {
		t.Fatal(err)
	}
	if len(reads) != 1 {
		t.Fatalf("credential CLI calls = %d, want 1", len(reads))
	}
}
