package main

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBoundOmniRejectsWrongIdentity_beforeCookieRotation(t *testing.T) {
	service, store, record, _ := maintenanceFixture(t)
	auth, err := authFromRecord(*record)
	if err != nil {
		t.Fatal(err)
	}
	var rotations, submissions atomic.Int32
	// The page reports a different Google account than the binding expects, so
	// the turn must stop before it rotates anything or submits.
	localSidecarAll(t, service, func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/app"):
			writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			if _, err := writer.Write([]byte(nativeIdentityPage(testOtherGaia))); err != nil {
				t.Error(err)
			}
		case request.URL.Path == "/RotateCookies":
			rotations.Add(1)
			writeRotationFixture(writer, request)
		default:
			submissions.Add(1)
			writeFixture(t, writer, `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"video/mp4","data":"dGVzdA=="}}]}}]}`)
		}
	})

	result := invoke(t, service, "executor.execute", executorRequest{AuthID: record.ID, AuthProvider: provider, Model: omniModel, Format: "gemini", SourceFormat: "gemini", Payload: []byte(`{"contents":[{"parts":[{"text":"video"}]}]}`), StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata})

	if result.OK || rotations.Load() != 0 || submissions.Load() != 0 || store.writes != 0 {
		t.Fatal("bound Omni rotated or submitted the wrong Google account")
	}
}

func TestBoundFlashRejectsReboundReference_beforeCredentialResolution(t *testing.T) {
	service, store, record, _ := maintenanceFixture(t)
	record.TokenRef = recordFixture(t, "b").TokenRef
	auth, err := authFromRecord(*record)
	if err != nil {
		t.Fatal(err)
	}

	result := invoke(t, service, "executor.execute", executorRequest{AuthID: record.ID, AuthProvider: provider, Model: flashModel, Format: "gemini", Payload: []byte(`{}`), StorageJSON: auth.StorageJSON})

	if result.OK || result.Error.Code != "binding_mismatch" || len(store.reads) != 0 {
		t.Fatal("bound Flash resolved another reference")
	}
}
