package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type captureFunc func(context.Context, captureRequest) (capturedSession, error)

func (capture captureFunc) Capture(ctx context.Context, request captureRequest) (capturedSession, error) {
	return capture(ctx, request)
}

func maintenanceFixture(t *testing.T) (*service, *memorySecrets, *storageRecord, *atomic.Int32) {
	t.Helper()
	record := recordFixture(t, "a")
	saves := &atomic.Int32{}
	readHost := accountHost(t, []storageRecord{record})
	service := newService(func(method string, raw []byte) ([]byte, error) {
		if method == "host.auth.save" {
			var request callbackRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Fatal(err)
			}
			if request.HostCallbackID != "scope-list" {
				t.Fatal("callback escaped request")
			}
			assertCanonicalStopPolicy(t, genericMetadataFromSavedJSON(t, request.JSON).RequestScopedErrors)
			saves.Add(1)
			return []byte(`{"ok":true,"result":{"name":"saved"}}`), nil
		}
		if method == "host.auth.get" {
			return jsonFixture(t, envelope{OK: true, Result: jsonFixture(t, struct {
				JSON storageRecord `json:"json"`
			}{record})}), nil
		}
		return readHost(method, raw)
	})
	store := &memorySecrets{tokens: map[string]sessionToken{record.TokenRef: {encodedToken("original")}}}
	service.secrets = store
	service.config.MaintenanceSources = map[string]maintenanceSource{record.ID: testMaintenanceSource()}
	service.source = captureFunc(func(context.Context, captureRequest) (capturedSession, error) {
		t.Error("unexpected source capture")
		return capturedSession{}, failure(503, "source_unavailable")
	})
	return service, store, &record, saves
}

func maintainFixture(t *testing.T, service *service, body string) maintenanceResponse {
	t.Helper()
	response, err := service.management(t.Context(), jsonFixture(t, managementRequest{Method: "POST", Path: "/v0/management" + maintainPath, Body: []byte(body), HostCallbackID: "scope-list"}))
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("maintain failed: %v %d %s", err, response.StatusCode, response.Body)
	}
	var result maintenanceResponse
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(response.Body), "op://") || strings.Contains(string(response.Body), "gemini-web:v1:") {
		t.Fatal("credential leaked")
	}
	return result
}

func TestMaintenanceRenewsAndSyncs_whenIdentityMatches(t *testing.T) {
	service, store, _, saves := maintenanceFixture(t)
	var inspections, renewals atomic.Int32
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/session/inspect":
			inspections.Add(1)
			writeFixture(t, writer, `{"account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
		case "/v1/session/renew":
			renewals.Add(1)
			writeFixture(t, writer, `{"token":"`+encodedToken("renewed")+`","account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
		default:
			t.Error("unexpected request")
		}
	})

	result := maintainFixture(t, service, `{}`)

	if len(result.Results) != 1 || result.Results[0].State != maintenanceReady || store.writes != 1 || saves.Load() != 1 || inspections.Load() != 1 || renewals.Load() != 1 {
		t.Fatalf("maintenance incomplete: %+v", result)
	}
}

func TestManualReplacementRejectsBusyReference_beforeHTTPOrSecrets(t *testing.T) {
	service, store, record, saves := maintenanceFixture(t)
	lease, err := service.acquireCredential(record.TokenRef, false)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.guard.RUnlock()
	var calls atomic.Int32
	localSidecar(t, service, func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeFixture(t, writer, `{"available":true}`)
	})

	response, err := service.management(t.Context(), jsonFixture(t, managementRequest{Method: "POST", Path: accountsPath, HostCallbackID: "scope-list", Body: jsonFixture(t, struct {
		Label, Token string
		ExistingID   string `json:"existing_id"`
	}{"Updated", encodedToken("new"), record.ID})}))

	if err != nil || response.StatusCode != 409 || calls.Load() != 0 || len(store.reads) != 0 || store.writes != 0 || saves.Load() != 0 {
		t.Fatalf("busy manual update had effects: %v %d", err, response.StatusCode)
	}
}

func TestMaintenanceCooldownSkipsGoogle_whenAuthenticationSourceFails(t *testing.T) {
	service, store, _, saves := maintenanceFixture(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return clock }
	var inspections, captures atomic.Int32
	service.source = captureFunc(func(context.Context, captureRequest) (capturedSession, error) {
		captures.Add(1)
		return capturedSession{}, failure(401, "source_login_required")
	})
	localSidecar(t, service, func(writer http.ResponseWriter, _ *http.Request) {
		inspections.Add(1)
		writer.WriteHeader(401)
		writeFixture(t, writer, `{"error":{"message":"auth_error"}}`)
	})
	first := maintainFixture(t, service, `{}`)
	if first.Results[0].State != maintenanceSourceRejected {
		t.Fatalf("unexpected first state: %+v", first)
	}
	start := clock
	for _, minutes := range []int{5, 10, 25} {
		clock = start.Add(time.Duration(minutes) * time.Minute)
		result := maintainFixture(t, service, `{}`)
		if result.Results[0].State != maintenanceCooldown || captures.Load() != 1 || inspections.Load() != 1 {
			t.Fatalf("cooldown retried at %d", minutes)
		}
	}
	clock = start.Add(30 * time.Minute)
	maintainFixture(t, service, `{}`)
	if captures.Load() != 2 || inspections.Load() != 2 || store.writes != 0 || saves.Load() != 0 {
		t.Fatal("cooldown boundary changed")
	}
}

func TestMaintenanceHasNoEffects_whenDisabledOrBusy(t *testing.T) {
	for _, scenario := range []string{"disabled", "busy"} {
		t.Run(scenario, func(t *testing.T) {
			service, store, record, saves := maintenanceFixture(t)
			want := maintenanceDisabled
			if scenario == "disabled" {
				record.Disabled = true
			} else {
				lease, err := service.acquireCredential(record.TokenRef, false)
				if err != nil {
					t.Fatal(err)
				}
				defer lease.guard.RUnlock()
				want = maintenanceBusy
			}
			localSidecar(t, service, func(http.ResponseWriter, *http.Request) { t.Error("skipped account reached HTTP") })

			result := maintainFixture(t, service, `{}`)

			if result.Results[0].State != want || len(store.reads) != 0 || store.writes != 0 || saves.Load() != 0 {
				t.Fatalf("skip had effects: %+v", result)
			}
		})
	}
}
