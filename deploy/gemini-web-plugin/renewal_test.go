package main

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type renewalStore struct {
	memorySecrets
	writeErr  error
	resultRef secretReference
	lastWrite secretWrite
	persisted atomic.Bool
}

func (store *renewalStore) ReplaceIfExpected(ctx context.Context, request secretReplacement) error {
	store.lastWrite = secretWrite{Token: request.Replacement, Existing: request.Reference}
	if store.writeErr != nil {
		store.writes++
		return store.writeErr
	}
	if err := store.memorySecrets.ReplaceIfExpected(ctx, request); err != nil {
		return err
	}
	store.persisted.Store(true)
	if store.resultRef.value != "" && store.resultRef != request.Reference {
		return failure(503, "secret_write_outcome_unknown")
	}
	return nil
}

func (store *renewalStore) Put(ctx context.Context, request secretWrite) (secretReference, error) {
	store.lastWrite = request
	if store.writeErr != nil {
		store.writes++
		return secretReference{}, store.writeErr
	}
	reference, err := store.memorySecrets.Put(ctx, request)
	store.persisted.Store(err == nil)
	if store.resultRef.value != "" {
		return store.resultRef, err
	}
	return reference, err
}

func TestOmniRenewsBeforeSubmission_whenSessionAcquisitionSucceeds(t *testing.T) {
	for _, changed := range []bool{true, false} {
		t.Run(map[bool]string{true: "changed", false: "unchanged"}[changed], func(t *testing.T) {
			service := newService(nil)
			record := recordFixture(t, "b")
			auth, err := authFromRecord(record)
			if err != nil {
				t.Fatal(err)
			}
			original := encodedToken("SID=synthetic-original; SAPISID=synthetic-sapi")
			renewed := original
			if changed {
				renewed = encodedToken("SID=synthetic-rotated; SAPISID=synthetic-sapi; SIDCC=fresh")
			}
			store := &renewalStore{memorySecrets: memorySecrets{tokens: map[string]sessionToken{record.TokenRef: {original}}}}
			service.secrets = store
			var renewals, submissions atomic.Int32
			localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != "POST" || request.Host != "gemini-web2api:8081" || request.Header.Get("Authorization") != "" {
					t.Error("private request boundary changed")
				}
				switch request.URL.Path {
				case "/v1/session/renew":
					renewals.Add(1)
					body, err := io.ReadAll(request.Body)
					if err != nil || string(body) != "{}" || request.Header.Get("x-goog-api-key") != original {
						t.Error("renewal contract changed")
					}
					writeFixture(t, writer, `{"token":"`+renewed+`"}`)
				case "/v1beta/models/gemini-web-omni:generateContent":
					submissions.Add(1)
					if request.Header.Get("x-goog-api-key") != renewed || changed && !store.persisted.Load() {
						t.Error("video preceded durable renewal")
					}
					writeFixture(t, writer, `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"video/mp4","data":"dGVzdA=="}}]}}]}`)
				default:
					t.Error("unexpected sidecar route")
				}
			})

			result := invoke(t, service, "executor.execute", executorRequest{AuthID: record.ID, AuthProvider: provider, Model: omniModel, Format: "gemini", SourceFormat: "gemini", Payload: []byte(`{"contents":[{"parts":[{"text":"synthetic video"}]}]}`), StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata})

			if !result.OK || renewals.Load() != 1 || submissions.Load() != 1 {
				t.Fatalf("preflight/submission failed: %+v", result.Error)
			}
			if len(store.reads) != 1 || store.reads[0] != record.TokenRef {
				t.Fatal("selected record was not retained")
			}
			if changed {
				if store.writes != 1 || store.lastWrite.Existing.value != record.TokenRef || store.lastWrite.Label != "" || store.lastWrite.Token.value != renewed {
					t.Fatal("renewal was not persisted to existing reference")
				}
			} else if store.writes != 0 {
				t.Fatal("unchanged token was persisted")
			}
			if strings.Contains(string(result.Result), "gemini-web:v1:") || strings.Contains(string(result.Result), "synthetic-original") || strings.Contains(string(auth.StorageJSON), "gemini-web:v1:") {
				t.Fatal("credential escaped private preflight")
			}
		})
	}
}

func TestOmniAbortsBeforeSubmission_whenRenewalOrPersistenceFails(t *testing.T) {
	original := encodedToken("SID=synthetic-original")
	renewed := encodedToken("SID=synthetic-original; SIDCC=fresh")
	otherIndex := "gemini-web:v1:" + base64.RawURLEncoding.EncodeToString([]byte(`{"cookie":"SID=synthetic-original","auth_user":3}`))
	for _, scenario := range []struct {
		name       string
		status     int
		body       string
		writeErr   error
		changedRef bool
		writes     int
	}{
		{"denied", 401, `{"token":"` + renewed + `","error":"synthetic-private"}`, nil, false, 0},
		{"redirect", 307, `{}`, nil, false, 0},
		{"malformed", 200, `{"token":"gemini-web:v1:invalid"}`, nil, false, 0},
		{"unknown-field", 200, `{"token":"` + renewed + `","cookie":"synthetic-private"}`, nil, false, 0},
		{"duplicate-field", 200, `{"token":"` + renewed + `","token":"` + original + `"}`, nil, false, 0},
		{"other-account-index", 200, `{"token":"` + otherIndex + `"}`, nil, false, 0},
		{"persist-failed", 200, `{"token":"` + renewed + `"}`, errors.New("synthetic-private"), false, 1},
		{"reference-changed", 200, `{"token":"` + renewed + `"}`, nil, true, 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service := newService(nil)
			record := recordFixture(t, "a")
			auth, err := authFromRecord(record)
			if err != nil {
				t.Fatal(err)
			}
			store := &renewalStore{memorySecrets: memorySecrets{tokens: map[string]sessionToken{record.TokenRef: {original}}}, writeErr: scenario.writeErr}
			if scenario.changedRef {
				store.resultRef = secretReference{value: recordFixture(t, "b").TokenRef}
			}
			service.secrets = store
			var renewals, submissions atomic.Int32
			localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/v1/session/renew" {
					renewals.Add(1)
					writer.Header().Set("Location", sidecarBase+"/v1/session/renew")
					writer.WriteHeader(scenario.status)
					writeFixture(t, writer, scenario.body)
					return
				}
				submissions.Add(1)
				writeFixture(t, writer, `{}`)
			})

			result := invoke(t, service, "executor.execute", executorRequest{AuthID: record.ID, AuthProvider: provider, Model: omniModel, Format: "gemini", SourceFormat: "gemini", Payload: []byte(`{"contents":[{"parts":[{"text":"synthetic video"}]}]}`), StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata})

			if result.OK || result.Error == nil || !strings.HasPrefix(result.Error.Code, "gemini_web_omni:") {
				t.Fatal("failure lost stop policy")
			}
			if renewals.Load() != 1 || submissions.Load() != 0 || store.writes != scenario.writes {
				t.Fatal("failed preflight reached video or retried")
			}
			raw := jsonFixture(t, result)
			if strings.Contains(string(raw), "synthetic") || strings.Contains(string(raw), "gemini-web:v1:") || strings.Contains(string(raw), "op://") {
				t.Fatal("private renewal data escaped")
			}
		})
	}
}

func TestExecutorHTTPDeniesPrivateRenewal_beforeSecrets(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		service := newService(nil)
		store := &memorySecrets{tokens: make(map[string]sessionToken)}
		service.secrets = store

		result := invoke(t, service, "executor.http_request", executorHTTPRequest{Method: method, URL: sidecarBase + "/v1/session/renew", AuthProvider: provider})

		if result.OK || len(store.reads) != 0 || store.writes != 0 {
			t.Fatal("private renewal exposed through public HTTP API")
		}
	}
}
