package main

import (
	"context"
	"errors"
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
			// The renewed token is derived from the rotated jar now, so the test
			// asks the rotation to happen instead of naming the result.
			cookie := "SID=synthetic-original; SAPISID=synthetic-sapi"
			original := encodedToken(cookie)
			renewed := original
			if changed {
				renewed = rotatedToken(cookie)
			}
			store := &renewalStore{memorySecrets: memorySecrets{tokens: map[string]sessionToken{record.TokenRef: {original}}}}
			service.secrets = store
			var renewals, submissions atomic.Int32
			localSidecarAll(t, service, func(writer http.ResponseWriter, request *http.Request) {
				switch sidecarPath(request) {
				case "/v1/session/inspect":
					writeIdentityFixture(t, writer)
				case "/v1/session/renew":
					renewals.Add(1)
					if !strings.Contains(request.Header.Get("Cookie"), "synthetic-original") {
						t.Error("rotation did not carry the credential jar")
					}
					if changed {
						writeRotationFixture(writer, request)
						return
					}
					writer.WriteHeader(http.StatusOK)
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
	// The renewal answer is a cookie jar, not a document, so the malformed-body
	// scenarios this table carried cannot occur any more. What can still go
	// wrong is the status, the account moving across the rotation, and the
	// persistence of the result.
	for _, scenario := range []struct {
		name       string
		status     int
		movesAway  bool
		writeErr   error
		changedRef bool
		writes     int
	}{
		{"denied", 401, false, nil, false, 0},
		{"redirect", 307, false, nil, false, 0},
		{"other-account", 200, true, nil, false, 0},
		{"persist-failed", 200, false, errors.New("synthetic-private"), false, 1},
		{"reference-changed", 200, false, nil, true, 1},
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
			var reads atomic.Int32
			localSidecarAll(t, service, func(writer http.ResponseWriter, request *http.Request) {
				if sidecarPath(request) == "/v1/session/inspect" {
					gaia := testGaia
					if scenario.movesAway && reads.Add(1) > 1 {
						gaia = testOtherGaia
					}
					writer.Header().Set("Content-Type", "text/html; charset=utf-8")
					if _, err := writer.Write([]byte(nativeIdentityPage(gaia))); err != nil {
						t.Error(err)
					}
					return
				}
				if sidecarPath(request) == "/v1/session/renew" {
					renewals.Add(1)
					if scenario.status != 200 {
						writer.Header().Set("Location", "https://accounts.google.com/signin")
						writer.WriteHeader(scenario.status)
						return
					}
					writeRotationFixture(writer, request)
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
