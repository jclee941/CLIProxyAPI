package main

import (
	"encoding/json"
	"strings"
	"testing"
)

type detachView struct {
	ID       string `json:"id"`
	Detached bool   `json:"detached"`
	Error    string `json:"error"`
}

func detachCall(t *testing.T, service *service, body string) (detachView, int) {
	t.Helper()
	response, err := service.management(t.Context(), jsonFixture(t, managementRequest{Method: "POST", Path: detachPath, HostCallbackID: "scope-detach", Body: []byte(body)}))
	if err != nil {
		t.Fatal(err)
	}
	var view detachView
	if err := json.Unmarshal(response.Body, &view); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(response.Body), "op://") || strings.Contains(string(response.Body), "gemini-web:v1:") {
		t.Fatal("credential leaked")
	}
	return view, response.StatusCode
}

func migratedAccountFixture(t *testing.T) (*service, *memorySecrets, storageRecord, maintenanceSource) {
	t.Helper()
	service, host, vault, _ := resolveFixture(t)
	previous := recordFixture(t, "a")
	previous.SessionRevision = 8
	host.records[previous.ID] = jsonFixture(t, previous)
	user := uint64(2)
	binding := maintenanceSource{TokenRef: previous.TokenRef, ProfileGUID: "00000000-0000-4000-8000-000000000001", ExpectedGaiaSHA256: testAccountDigest, AuthUser: &user}
	service.config.MaintenanceSources = map[string]maintenanceSource{previous.ID: binding}
	started, status := loginCall(t, service, "start", jsonFixture(t, struct {
		Label      string `json:"label"`
		ExistingID string `json:"existing_id"`
		Consent    bool   `json:"consent"`
	}{"Migrated", previous.ID, true}))
	if status != 200 {
		t.Fatalf("start status=%d error=%s", status, started.Error)
	}
	view := completeFixture(t, service, started)
	record, err := service.parseStorage(host.records[view.AccountID], true)
	if err != nil {
		t.Fatal(err)
	}
	local, err := service.localStore().read(record.TokenRef)
	if err != nil || local.LegacyRef != previous.TokenRef {
		t.Fatalf("fixture did not migrate a legacy binding: ref=%q err=%v", local.LegacyRef, err)
	}
	return service, vault, record, binding
}

func TestDetachKeepsAccountUsableWhileBindingRemains_thenAfterRemoval(t *testing.T) {
	service, vault, record, _ := migratedAccountFixture(t)

	view, status := detachCall(t, service, `{"id":"`+record.ID+`","consent":true}`)

	if status != 200 || view.ID != record.ID || !view.Detached {
		t.Fatalf("detach status=%d view=%+v", status, view)
	}
	local, err := service.localStore().read(record.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if local.LegacyRef != "" || local.LegacyGUID != "" || local.LegacyUserBound {
		t.Fatalf("legacy binding retained: %+v", local)
	}
	if err := service.checkLocalBinding(record, local); err != nil {
		t.Fatalf("detached record rejected while the config binding still exists: %v", err)
	}
	service.config.MaintenanceSources = map[string]maintenanceSource{}
	if err := service.checkLocalBinding(record, local); err != nil {
		t.Fatalf("detached record rejected after binding removal: %v", err)
	}
	if vault.writes != 0 || len(vault.reads) != 0 {
		t.Fatalf("detach touched the vault: reads=%d writes=%d", len(vault.reads), vault.writes)
	}
}

func TestDetachIsIdempotent_whenAlreadyDetached(t *testing.T) {
	service, _, record, _ := migratedAccountFixture(t)

	if _, status := detachCall(t, service, `{"id":"`+record.ID+`","consent":true}`); status != 200 {
		t.Fatalf("first detach status=%d", status)
	}
	view, status := detachCall(t, service, `{"id":"`+record.ID+`","consent":true}`)

	if status != 200 || !view.Detached {
		t.Fatalf("second detach status=%d view=%+v", status, view)
	}
}

func TestDetachPreservesDriftGuard_whenRecordWasNeverDetached(t *testing.T) {
	service, _, _, record := localAccountFixture(t, false)
	local, err := service.localStore().read(record.TokenRef)
	if err != nil || local.LegacyRef != "" {
		t.Fatalf("fixture is not a native local record: ref=%q err=%v", local.LegacyRef, err)
	}
	user := uint64(2)
	service.config.MaintenanceSources = map[string]maintenanceSource{record.ID: {TokenRef: "op://homelab/" + strings.Repeat("z", 26) + "/web-session", ProfileGUID: "00000000-0000-4000-8000-000000000009", ExpectedGaiaSHA256: testAccountDigest, AuthUser: &user}}

	err = service.checkLocalBinding(record, local)

	if err == nil || safeCredentialCode(err) != "binding_mismatch" {
		t.Fatalf("drift guard weakened: %v", err)
	}
}

func TestDetachRejectsUnsafeRequests_beforeWriting(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"missing_consent", `{"id":"ACCOUNT"}`, 400, "invalid_detach_request"},
		{"denied_consent", `{"id":"ACCOUNT","consent":false}`, 400, "invalid_detach_request"},
		{"unknown_field", `{"id":"ACCOUNT","consent":true,"force":true}`, 400, "invalid_detach_request"},
		{"empty_body", `{}`, 400, "invalid_detach_request"},
		{"unknown_account", `{"id":"gemini-web-unknown.json","consent":true}`, 404, "account_not_found"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service, vault, record, binding := migratedAccountFixture(t)

			view, status := detachCall(t, service, strings.ReplaceAll(scenario.body, "ACCOUNT", record.ID))

			if status != scenario.status || view.Error != scenario.code {
				t.Fatalf("status=%d view=%+v want=%d/%s", status, view, scenario.status, scenario.code)
			}
			local, err := service.localStore().read(record.TokenRef)
			if err != nil || local.LegacyRef != binding.TokenRef {
				t.Fatalf("unsafe detach mutated the record: ref=%q err=%v", local.LegacyRef, err)
			}
			if vault.writes != 0 {
				t.Fatal("unsafe detach wrote a credential")
			}
		})
	}
}

func TestDetachRefusesInterruptedSession_soUncertaintyIsPreserved(t *testing.T) {
	service, _, record, binding := migratedAccountFixture(t)
	local, err := service.localStore().read(record.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	local.State = localRenewing
	if err := service.localStore().write(local); err != nil {
		t.Fatal(err)
	}

	view, status := detachCall(t, service, `{"id":"`+record.ID+`","consent":true}`)

	if status != 409 || view.Error != "detach_requires_ready_session" {
		t.Fatalf("status=%d view=%+v", status, view)
	}
	after, err := service.localStore().read(record.TokenRef)
	if err != nil || after.LegacyRef != binding.TokenRef || after.State != localRenewing {
		t.Fatalf("interrupted record changed: ref=%q state=%s err=%v", after.LegacyRef, after.State, err)
	}
}
