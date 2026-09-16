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

// migratedAccountFixture stamps the legacy markers a pre-local account carried
// into its session, which is the only state the detach route still acts on.
func migratedAccountFixture(t *testing.T) (*service, storageRecord, string) {
	t.Helper()
	service, _, record := localAccountFixture(t, false)
	local, err := service.localStore().read(record.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	legacy := "op://homelab/" + strings.Repeat("a", 26) + "/web-session"
	local.LegacyRef, local.LegacyGUID, local.LegacyUserBound = legacy, "00000000-0000-4000-8000-000000000001", true
	if err := service.localStore().write(local); err != nil {
		t.Fatal(err)
	}
	return service, record, legacy
}

func TestDetachKeepsAccountUsableWhileBindingRemains_thenAfterRemoval(t *testing.T) {
	service, record, _ := migratedAccountFixture(t)

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
	if err := service.checkLocalBinding(record, local); err != nil {
		t.Fatalf("detached record rejected after binding removal: %v", err)
	}
}

func TestDetachIsIdempotent_whenAlreadyDetached(t *testing.T) {
	service, record, _ := migratedAccountFixture(t)

	if _, status := detachCall(t, service, `{"id":"`+record.ID+`","consent":true}`); status != 200 {
		t.Fatalf("first detach status=%d", status)
	}
	view, status := detachCall(t, service, `{"id":"`+record.ID+`","consent":true}`)

	if status != 200 || !view.Detached {
		t.Fatalf("second detach status=%d view=%+v", status, view)
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
			service, record, legacy := migratedAccountFixture(t)

			view, status := detachCall(t, service, strings.ReplaceAll(scenario.body, "ACCOUNT", record.ID))

			if status != scenario.status || view.Error != scenario.code {
				t.Fatalf("status=%d view=%+v want=%d/%s", status, view, scenario.status, scenario.code)
			}
			local, err := service.localStore().read(record.TokenRef)
			if err != nil || local.LegacyRef != legacy {
				t.Fatalf("unsafe detach mutated the record: ref=%q err=%v", local.LegacyRef, err)
			}
		})
	}
}

func TestDetachRefusesInterruptedSession_soUncertaintyIsPreserved(t *testing.T) {
	service, record, legacy := migratedAccountFixture(t)
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
	if err != nil || after.LegacyRef != legacy || after.State != localRenewing {
		t.Fatalf("interrupted record changed: ref=%q state=%s err=%v", after.LegacyRef, after.State, err)
	}
}
