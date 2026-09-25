package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func maintenanceFixture(t *testing.T) (*service, *storageRecord, *atomic.Int32) {
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
	seedSessions(t, service, map[string]sessionToken{record.TokenRef: {encodedToken("original")}})
	return service, &record, saves
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
	if strings.Contains(string(response.Body), "gemini-web:v1:") {
		t.Fatal("credential leaked")
	}
	return result
}

func TestMaintenanceHasNoEffects_whenDisabledOrBusy(t *testing.T) {
	for _, scenario := range []string{"disabled", "busy"} {
		t.Run(scenario, func(t *testing.T) {
			service, record, saves := maintenanceFixture(t)
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

			if result.Results[0].State != want || saves.Load() != 0 {
				t.Fatalf("skip had effects: %+v", result)
			}
		})
	}
}
