package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMaintenanceCapturesOnlyAuthenticationFailure_whenInspectFails(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		status   int
		body     string
		captures int32
	}{
		{"auth", 401, `{"error":{"code":401,"message":"auth_error"}}`, 1},
		{"forbidden", 403, `{"error":{"message":"auth_error"}}`, 0},
		{"rate", 429, `{"error":{"message":"auth_error"}}`, 0},
		{"bootstrap", 502, `{"error":{"message":"bootstrap_failed"}}`, 0},
		{"parser", 200, `{"unknown":"private"}`, 0},
		{"missing-identity", 200, `{"auth_user":2}`, 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service, store, record, saves := maintenanceFixture(t)
			var captures, inspections atomic.Int32
			service.source = captureFunc(func(_ context.Context, request captureRequest) (capturedSession, error) {
				captures.Add(1)
				if request.AuthUser != 2 || request.Binding.TokenRef != record.TokenRef || len(request.Bindings) != 1 {
					t.Error("source binding lost")
				}
				return capturedSession{Token: sessionToken{encodedToken("captured")}, AccountSHA256: testAccountDigest}, nil
			})
			localSidecarAll(t, service, func(writer http.ResponseWriter, request *http.Request) {
				if !strings.HasSuffix(request.URL.Path, "/app") {
					t.Errorf("recovery attempted %s", request.URL.Path)
				}
				inspections.Add(1)
				if strings.Contains(request.Header.Get("Cookie"), "captured") {
					writeIdentityFixture(t, writer)
					return
				}
				writer.WriteHeader(scenario.status)
				writeFixture(t, writer, scenario.body)
			})

			result := maintainFixture(t, service, `{}`)

			if captures.Load() != scenario.captures {
				t.Fatalf("unsafe source fallback: %+v", result)
			}
			if scenario.captures == 1 {
				if result.Results[0].State != maintenanceReady || inspections.Load() != 2 || store.writes != 1 || saves.Load() != 1 {
					t.Fatalf("capture not verified and saved: %+v", result)
				}
			} else if store.writes != 0 || saves.Load() != 0 {
				t.Fatal("failed inspection persisted")
			}
		})
	}
}

func TestMaintenanceRejectsIdentityMismatch_whenCaptureOrHTTPDisagrees(t *testing.T) {
	// The account index is derived from the token now, so an endpoint can no
	// longer report a different one; only the hash and the binding can disagree.
	for _, scenario := range []string{"source-hash", "source-index", "http-hash", "binding-index", "binding-ref", "renew-hash"} {
		t.Run(scenario, func(t *testing.T) {
			service, store, record, saves := maintenanceFixture(t)
			var calls atomic.Int32
			if scenario == "binding-index" {
				index := uint64(0)
				binding := testMaintenanceSource()
				binding.AuthUser = &index
				service.config.MaintenanceSources[record.ID] = binding
			}
			if scenario == "binding-ref" {
				record.TokenRef = recordFixture(t, "b").TokenRef
			}
			service.source = captureFunc(func(context.Context, captureRequest) (capturedSession, error) {
				token := sessionToken{encodedToken("captured")}
				digest := testAccountDigest
				if scenario == "source-hash" {
					digest = testOtherAccountDigest
				}
				if scenario == "source-index" {
					token.value = "gemini-web:v1:" + base64.RawURLEncoding.EncodeToString([]byte(`{"cookie":"captured","auth_user":0}`))
				}
				return capturedSession{Token: token, AccountSHA256: digest}, nil
			})
			localSidecarAll(t, service, func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.URL.Path == "/RotateCookies" {
					writeRotationFixture(writer, request)
					return
				}
				if strings.HasPrefix(scenario, "source-") || strings.HasPrefix(scenario, "http-") {
					if !strings.Contains(request.Header.Get("Cookie"), "captured") {
						writer.WriteHeader(401)
						return
					}
				}
				gaia := testGaia
				if scenario == "http-hash" || scenario == "renew-hash" {
					gaia = testOtherGaia
				}
				writer.Header().Set("Content-Type", "text/html; charset=utf-8")
				if _, err := writer.Write([]byte(nativeIdentityPage(gaia))); err != nil {
					t.Error(err)
				}
			})

			result := maintainFixture(t, service, `{}`)

			if result.Results[0].State == maintenanceReady || store.writes != 0 || saves.Load() != 0 {
				t.Fatalf("identity mismatch persisted: %+v", result)
			}
			if strings.HasPrefix(scenario, "binding-") && calls.Load() != 0 {
				t.Fatal("bad binding reached HTTP")
			}
		})
	}
}

func TestMaintenanceRetriesOnlyHostSync_whenSecretAlreadyChanged(t *testing.T) {
	service, store, record, saves := maintenanceFixture(t)
	host := service.host
	var failed atomic.Bool
	service.host = func(method string, raw []byte) ([]byte, error) {
		if method == "host.auth.save" && !failed.Swap(true) {
			return nil, errors.New("synthetic host failure")
		}
		if method == "host.auth.save" {
			var request callbackRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Fatal(err)
			}
			var stored storageRecord
			if err := json.Unmarshal(request.JSON, &stored); err != nil {
				t.Fatal(err)
			}
			if stored.Label != "Current host label" {
				t.Error("stale host metadata overwritten")
			}
		}
		return host(method, raw)
	}
	var inspections, renewals atomic.Int32
	localSidecarAll(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if sidecarPath(request) == "/v1/session/renew" {
			// Only the first pass rotates; the retry is about the host sync, so
			// a second rotation would change the token again and mask that.
			if renewals.Add(1) == 1 {
				writeRotationFixture(writer, request)
				return
			}
			writer.WriteHeader(http.StatusOK)
			return
		}
		inspections.Add(1)
		writeIdentityFixture(t, writer)
	})
	// The renewed token is whatever the rotation produced, so the check is that
	// it persisted rather than reverting to the value the pass started from.
	before := store.tokens[record.TokenRef].value
	first := maintainFixture(t, service, `{}`)
	if first.Results[0].State != maintenanceHostPending || store.tokens[record.TokenRef].value == before {
		t.Fatal("host failure rolled back credential")
	}
	record.Label = "Current host label"

	result := maintainFixture(t, service, `{}`)

	// A host-pending retry re-proves the identity and syncs the host; it must not
	// reach the rotation endpoint, because rotating again would mint a second
	// token and lose the one the host is still owed.
	if result.Results[0].State != maintenanceReady || store.writes != 1 || renewals.Load() != 1 || inspections.Load() < 2 || saves.Load() != 1 {
		t.Fatalf("host retry rotated again: %+v writes=%d renewals=%d inspections=%d saves=%d", result, store.writes, renewals.Load(), inspections.Load(), saves.Load())
	}
}

func TestMaintenanceAbortsStaleWrite_whenExternalTokenChangesDuringRenewal(t *testing.T) {
	service, store, record, saves := maintenanceFixture(t)
	localSidecarAll(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if sidecarPath(request) == "/v1/session/renew" {
			store.mu.Lock()
			store.tokens[record.TokenRef] = sessionToken{encodedToken("external")}
			store.mu.Unlock()
			writeRotationFixture(writer, request)
			return
		}
		writeIdentityFixture(t, writer)
	})

	result := maintainFixture(t, service, `{}`)

	if result.Results[0].Error != "credential_changed" || store.writes != 0 || saves.Load() != 0 || store.tokens[record.TokenRef].value != encodedToken("external") {
		t.Fatalf("stale token overwrote newer value: %+v", result)
	}
}
