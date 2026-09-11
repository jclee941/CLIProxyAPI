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
		{"generic401", 401, `{"error":{"message":"bootstrap_failed"}}`, 0},
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
				return capturedSession{Token: sessionToken{encodedToken("captured")}, AccountSHA256: strings.Repeat("a", 64)}, nil
			})
			localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/v1/session/inspect" {
					t.Error("recovery attempted renewal")
				}
				inspections.Add(1)
				if request.Header.Get("x-goog-api-key") == encodedToken("captured") {
					writeFixture(t, writer, `{"account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
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
	for _, scenario := range []string{"source-hash", "source-index", "http-hash", "http-index", "binding-index", "binding-ref", "renew-hash", "renew-index"} {
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
				digest := strings.Repeat("a", 64)
				if scenario == "source-hash" {
					digest = strings.Repeat("b", 64)
				}
				if scenario == "source-index" {
					token.value = "gemini-web:v1:" + base64.RawURLEncoding.EncodeToString([]byte(`{"cookie":"captured","auth_user":0}`))
				}
				return capturedSession{Token: token, AccountSHA256: digest}, nil
			})
			localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if strings.HasPrefix(scenario, "source-") || strings.HasPrefix(scenario, "http-") {
					if request.Header.Get("x-goog-api-key") == encodedToken("original") {
						writer.WriteHeader(401)
						writeFixture(t, writer, `{"error":{"message":"auth_error"}}`)
						return
					}
				}
				digest, index := strings.Repeat("a", 64), uint64(2)
				if scenario == "http-hash" || scenario == "renew-hash" && request.URL.Path == "/v1/session/renew" {
					digest = strings.Repeat("b", 64)
				}
				if scenario == "http-index" || scenario == "renew-index" && request.URL.Path == "/v1/session/renew" {
					index = 0
				}
				if request.URL.Path == "/v1/session/renew" {
					writeFixture(t, writer, string(jsonFixture(t, struct {
						Token string `json:"token"`
						credentialInspection
					}{encodedToken("renewed"), credentialInspection{digest, index}})))
					return
				}
				writeFixture(t, writer, string(jsonFixture(t, credentialInspection{digest, index})))
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
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/session/renew" {
			renewals.Add(1)
			writeFixture(t, writer, `{"token":"`+encodedToken("renewed")+`","account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
			return
		}
		inspections.Add(1)
		writeFixture(t, writer, `{"account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
	})
	first := maintainFixture(t, service, `{}`)
	if first.Results[0].State != maintenanceHostPending || store.tokens[record.TokenRef].value != encodedToken("renewed") {
		t.Fatal("host failure rolled back credential")
	}
	record.Label = "Current host label"

	result := maintainFixture(t, service, `{}`)

	if result.Results[0].State != maintenanceReady || store.writes != 1 || renewals.Load() != 1 || inspections.Load() != 2 || saves.Load() != 1 {
		t.Fatalf("host retry rotated again: %+v", result)
	}
}

func TestMaintenanceAbortsStaleWrite_whenExternalTokenChangesDuringRenewal(t *testing.T) {
	service, store, record, saves := maintenanceFixture(t)
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/session/renew" {
			store.mu.Lock()
			store.tokens[record.TokenRef] = sessionToken{encodedToken("external")}
			store.mu.Unlock()
			writeFixture(t, writer, `{"token":"`+encodedToken("renewed")+`","account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
			return
		}
		writeFixture(t, writer, `{"account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
	})

	result := maintainFixture(t, service, `{}`)

	if result.Results[0].Error != "credential_changed" || store.writes != 0 || saves.Load() != 0 || store.tokens[record.TokenRef].value != encodedToken("external") {
		t.Fatalf("stale token overwrote newer value: %+v", result)
	}
}
