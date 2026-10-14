package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type resolveObserver struct {
	mu    sync.Mutex
	mode  string
	calls int
}

func (observer *resolveObserver) set(mode string) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.mode = mode
}

func (observer *resolveObserver) observe() string {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.calls++
	return observer.mode
}

func (observer *resolveObserver) count() int {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return observer.calls
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

func resolveFixture(t *testing.T) (*service, *loginHostFixture, *resolveObserver) {
	t.Helper()
	service, host := loginFixture(t)
	observer := &resolveObserver{mode: "healthy"}
	localWebAll(t, service, func(writer http.ResponseWriter, request *http.Request) {
		mode := observer.observe()
		switch credentialRoute(request) {
		case "credential:inspect":
			switch mode {
			case "probe_unknown":
				rejectionFixture(t, writer, 500, `{"error":{"code":500,"message":"probe_unavailable"}}`)
			case "probe_rejected":
				rejectionFixture(t, writer, 401, `{"error":{"code":401,"message":"auth_error"}}`)
			default:
				writeIdentityFixture(t, writer)
			}
		case "credential:renew":
			if mode == "renew_unknown" {
				rejectionFixture(t, writer, 500, `{"error":{"code":500,"message":"renew_unavailable"}}`)
				return
			}
			writeRotationFixture(writer, request)
		default:
			if !serveAccountRPC(t, writer, request) {
				t.Errorf("unexpected observer path %s", request.URL.Path)
				writer.WriteHeader(404)
			}
		}
	})
	return service, host, observer
}

func localAccountFixture(t *testing.T, interrupted bool) (*service, *resolveObserver, storageRecord) {
	t.Helper()
	service, host, observer := resolveFixture(t)
	started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
	ready := completeFixture(t, service, started)
	record, err := service.parseStorage(host.records[ready.AccountID], true)
	if err != nil {
		t.Fatal(err)
	}
	expected := localReady
	if interrupted {
		observer.set("renew_unknown")
		trapped := maintainFixture(t, service, `{"id":"`+record.ID+`"}`)
		if len(trapped.Results) != 1 || trapped.Results[0].State != maintenanceOperator || trapped.Results[0].Error != "session_renewal_outcome_unknown_requires_operator" {
			t.Fatalf("fixture did not trap an interrupted renewal: %+v", trapped.Results)
		}
		observer.set("healthy")
		expected = localRenewing
	}
	local, err := service.localStore().read(record.TokenRef)
	if err != nil || local.State != expected {
		t.Fatalf("fixture state=%s want=%s err=%v", local.State, expected, err)
	}
	return service, observer, record
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
	service, observer, record := localAccountFixture(t, true)
	observer.set("probe_unknown")

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
	service, observer, record := localAccountFixture(t, true)
	observer.set("probe_rejected")

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
			service, observer, record := localAccountFixture(t, scenario.interrupted)
			before := observer.count()
			expected := localReady
			if scenario.interrupted {
				expected = localRenewing
			}

			view, status := resolveCall(t, service, strings.ReplaceAll(scenario.body, "ACCOUNT", record.ID))

			if status != scenario.status || view.Error != scenario.code {
				t.Fatalf("status=%d view=%+v want=%d/%s", status, view, scenario.status, scenario.code)
			}
			if observer.count() != before {
				t.Fatalf("unsafe request reached the credential path: calls=%d", observer.count()-before)
			}
			local, err := service.localStore().read(record.TokenRef)
			if err != nil || local.State != expected {
				t.Fatalf("unsafe request changed state: state=%s want=%s err=%v", local.State, expected, err)
			}
		})
	}
}
