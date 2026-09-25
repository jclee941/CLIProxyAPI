package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type resolveSidecar struct {
	mu    sync.Mutex
	mode  string
	calls int
}

func (sidecar *resolveSidecar) set(mode string) {
	sidecar.mu.Lock()
	defer sidecar.mu.Unlock()
	sidecar.mode = mode
}

func (sidecar *resolveSidecar) observe() string {
	sidecar.mu.Lock()
	defer sidecar.mu.Unlock()
	sidecar.calls++
	return sidecar.mode
}

func (sidecar *resolveSidecar) count() int {
	sidecar.mu.Lock()
	defer sidecar.mu.Unlock()
	return sidecar.calls
}

type resolveView struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	Credential string `json:"credential"`
	Enabled    bool   `json:"enabled"`
	Error      string `json:"error"`
}

func rejectionFixture(t *testing.T, writer http.ResponseWriter, status int, body string) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if _, err := io.WriteString(writer, body); err != nil {
		t.Error(err)
	}
}

func resolveFixture(t *testing.T) (*service, *loginHostFixture, *resolveSidecar) {
	t.Helper()
	service, host := loginFixture(t)
	sidecar := &resolveSidecar{mode: "healthy"}
	localSidecarAll(t, service, func(writer http.ResponseWriter, request *http.Request) {
		mode := sidecar.observe()
		switch sidecarPath(request) {
		case "/v1/session/inspect":
			switch mode {
			case "probe_unknown":
				rejectionFixture(t, writer, 500, `{"error":{"code":500,"message":"probe_unavailable"}}`)
			case "probe_rejected":
				rejectionFixture(t, writer, 401, `{"error":{"code":401,"message":"auth_error"}}`)
			default:
				writeIdentityFixture(t, writer)
			}
		case "/v1/account-models":
			writeFixture(t, writer, `{"available":true,"observed_at":1234,"models":[{"capability_id":"actual","display_name":"3.8 Flash","mode":1}]}`)
		case "/v1/usage":
			writeFixture(t, writer, `{"tier":"PRO","tier_code":2,"metrics":[{"remaining_units":2400,"usage_fraction":0.25,"reset_unix_seconds":1789363992,"window_kind":"5h","unit":"provider_compute_unit"},{"remaining_units":48384,"usage_fraction":0,"reset_unix_seconds":1789950792,"window_kind":"weekly","unit":"provider_compute_unit"}],"source":"GoogleWeb","estimated":false,"observed_at":1789350000}`)
		case "/v1/session/renew":
			if mode == "renew_unknown" {
				rejectionFixture(t, writer, 500, `{"error":{"code":500,"message":"renew_unavailable"}}`)
				return
			}
			writeRotationFixture(writer, request)
		default:
			t.Errorf("unexpected sidecar path %s", request.URL.Path)
			writer.WriteHeader(404)
		}
	})
	return service, host, sidecar
}

func localAccountFixture(t *testing.T, interrupted bool) (*service, *resolveSidecar, storageRecord) {
	t.Helper()
	service, host, sidecar := resolveFixture(t)
	started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
	ready := completeFixture(t, service, started)
	record, err := service.parseStorage(host.records[ready.AccountID], true)
	if err != nil {
		t.Fatal(err)
	}
	expected := localReady
	if interrupted {
		sidecar.set("renew_unknown")
		trapped := maintainFixture(t, service, `{"id":"`+record.ID+`"}`)
		if len(trapped.Results) != 1 || trapped.Results[0].State != maintenanceOperator || trapped.Results[0].Error != "session_renewal_outcome_unknown_requires_operator" {
			t.Fatalf("fixture did not trap an interrupted renewal: %+v", trapped.Results)
		}
		sidecar.set("healthy")
		expected = localRenewing
	}
	local, err := service.localStore().read(record.TokenRef)
	if err != nil || local.State != expected {
		t.Fatalf("fixture state=%s want=%s err=%v", local.State, expected, err)
	}
	return service, sidecar, record
}

func resolveCall(t *testing.T, service *service, body string) (resolveView, int) {
	t.Helper()
	response, err := service.management(t.Context(), jsonFixture(t, managementRequest{Method: "POST", Path: resolvePath, HostCallbackID: "scope-resolve", Body: []byte(body)}))
	if err != nil {
		t.Fatal(err)
	}
	var view resolveView
	if err := json.Unmarshal(response.Body, &view); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(response.Body), "op://") || strings.Contains(string(response.Body), "gemini-web:v1:") {
		t.Fatal("credential leaked")
	}
	return view, response.StatusCode
}

func TestResolveReleasesInterruptedRenewal_whenStoredCredentialRemainsValid(t *testing.T) {
	service, _, record := localAccountFixture(t, true)

	view, status := resolveCall(t, service, `{"id":"`+record.ID+`","consent":true}`)

	if status != 200 || view.ID != record.ID || view.State != string(maintenanceReady) || view.Credential != "valid" || !view.Enabled {
		t.Fatalf("resolve status=%d view=%+v", status, view)
	}
	local, err := service.localStore().read(record.TokenRef)
	if err != nil || local.State != localReady {
		t.Fatalf("intent not released: state=%s err=%v", local.State, err)
	}
	resumed := maintainFixture(t, service, `{"id":"`+record.ID+`"}`)
	if len(resumed.Results) != 1 || resumed.Results[0].State != maintenanceReady {
		t.Fatalf("account did not resume maintenance: %+v", resumed.Results)
	}
}

func TestResolveKeepsInterruptedState_whenProbeOutcomeStaysUnknown(t *testing.T) {
	service, sidecar, record := localAccountFixture(t, true)
	sidecar.set("probe_unknown")

	view, status := resolveCall(t, service, `{"id":"`+record.ID+`","consent":true}`)

	if status != 409 || view.Error != "session_outcome_still_unknown" {
		t.Fatalf("ambiguous probe released the intent: status=%d view=%+v", status, view)
	}
	local, err := service.localStore().read(record.TokenRef)
	if err != nil || local.State != localRenewing {
		t.Fatalf("ambiguous probe changed state: state=%s err=%v", local.State, err)
	}
}

func TestResolveFencesRejectedCredential_whenProbeFailsAuthentication(t *testing.T) {
	service, sidecar, record := localAccountFixture(t, true)
	sidecar.set("probe_rejected")

	view, status := resolveCall(t, service, `{"id":"`+record.ID+`","consent":true}`)

	if status != 200 || view.State != string(maintenanceCooldown) || view.Credential != "auth_error" {
		t.Fatalf("rejected probe did not fence the account: status=%d view=%+v", status, view)
	}
	local, err := service.localStore().read(record.TokenRef)
	if err != nil || local.State != localReady {
		t.Fatalf("rejected probe left an intent: state=%s err=%v", local.State, err)
	}
	state := service.leases.get(record.TokenRef).snapshot()
	if state.state != maintenanceCooldown || state.errCode != "auth_error" {
		t.Fatalf("lease not fenced: %+v", state)
	}
}

func TestResolveRejectsUnsafeRequests_beforeProbingCredential(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		body        string
		interrupted bool
		status      int
		code        string
	}{
		{"missing_consent", `{"id":"ACCOUNT"}`, true, 400, "invalid_resolve_request"},
		{"denied_consent", `{"id":"ACCOUNT","consent":false}`, true, 400, "invalid_resolve_request"},
		{"unknown_field", `{"id":"ACCOUNT","consent":true,"force":true}`, true, 400, "invalid_resolve_request"},
		{"duplicate_id", `{"id":"ACCOUNT","id":"ACCOUNT","consent":true}`, true, 400, "invalid_resolve_request"},
		{"empty_body", `{}`, true, 400, "invalid_resolve_request"},
		{"invalid_id", `{"id":"../etc/passwd","consent":true}`, true, 400, "invalid_account_id"},
		{"unknown_account", `{"id":"gemini-web-unknown.json","consent":true}`, true, 404, "account_not_found"},
		{"uninterrupted_account", `{"id":"ACCOUNT","consent":true}`, false, 409, "resolve_requires_interrupted_operation"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service, sidecar, record := localAccountFixture(t, scenario.interrupted)
			before := sidecar.count()
			expected := localReady
			if scenario.interrupted {
				expected = localRenewing
			}

			view, status := resolveCall(t, service, strings.ReplaceAll(scenario.body, "ACCOUNT", record.ID))

			if status != scenario.status || view.Error != scenario.code {
				t.Fatalf("status=%d view=%+v want=%d/%s", status, view, scenario.status, scenario.code)
			}
			if sidecar.count() != before {
				t.Fatalf("unsafe request reached the credential path: calls=%d", sidecar.count()-before)
			}
			local, err := service.localStore().read(record.TokenRef)
			if err != nil || local.State != expected {
				t.Fatalf("unsafe request changed state: state=%s want=%s err=%v", local.State, expected, err)
			}
		})
	}
}
