package main

import (
	"encoding/json"
	"strings"
	"testing"
)

type labelView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	State string `json:"state"`
	Error string `json:"error"`
}

func labelCall(t *testing.T, service *service, body string) (labelView, int) {
	t.Helper()
	response, err := service.management(t.Context(), jsonFixture(t, managementRequest{Method: "POST", Path: labelPath, HostCallbackID: "scope-label", Body: []byte(body)}))
	if err != nil {
		t.Fatal(err)
	}
	var view labelView
	if err := json.Unmarshal(response.Body, &view); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(response.Body), "op://") || strings.Contains(string(response.Body), "gemini-web:v1:") {
		t.Fatal("credential leaked")
	}
	return view, response.StatusCode
}

func TestLabelRenamesAccountWithoutTouchingCredential_whenSessionIsReady(t *testing.T) {
	service, _, record := localAccountFixture(t, false)
	before, err := service.localStore().read(record.TokenRef)
	if err != nil {
		t.Fatal(err)
	}

	view, status := labelCall(t, service, `{"id":"`+record.ID+`","label":"operator@example.com"}`)

	if status != 200 || view.ID != record.ID || view.Label != "operator@example.com" {
		t.Fatalf("label status=%d view=%+v", status, view)
	}
	local, err := service.localStore().read(record.TokenRef)
	if err == nil && local.Token != before.Token {
		t.Fatal("rename replaced the credential")
	}
	if err != nil || local.State != localReady {
		t.Fatalf("record not ready after rename: state=%s err=%v", local.State, err)
	}
	if local.Target.Label != "operator@example.com" {
		t.Fatalf("stored label=%s", local.Target.Label)
	}
	if local.Target.SessionRevision != record.SessionRevision+1 {
		t.Fatalf("revision %d did not advance from %d", local.Target.SessionRevision, record.SessionRevision)
	}
	if shown := accountLabel(t, service, record.ID); shown != "operator@example.com" {
		t.Fatalf("dashboard label=%q", shown)
	}
}

func accountLabel(t *testing.T, service *service, id string) string {
	t.Helper()
	response, err := service.management(t.Context(), jsonFixture(t, managementRequest{Method: "GET", Path: accountsPath, HostCallbackID: "scope-label-list"}))
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("accounts failed: %v %d", err, response.StatusCode)
	}
	var body struct {
		Accounts []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatal(err)
	}
	for _, account := range body.Accounts {
		if account.ID == id {
			return account.Label
		}
	}
	t.Fatalf("account %s missing from dashboard listing", id)
	return ""
}

func TestLabelRejectsUnsafeRequests_beforeWriting(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		body        string
		interrupted bool
		status      int
		code        string
	}{
		{"empty_label", `{"id":"ACCOUNT","label":""}`, false, 400, "invalid_label_request"},
		{"blank_label", `{"id":"ACCOUNT","label":"   "}`, false, 400, "invalid_label_request"},
		{"control_characters", `{"id":"ACCOUNT","label":"bad\nlabel"}`, false, 400, "invalid_label_request"},
		{"missing_label", `{"id":"ACCOUNT"}`, false, 400, "invalid_label_request"},
		{"unknown_field", `{"id":"ACCOUNT","label":"a@b.com","force":true}`, false, 400, "invalid_label_request"},
		{"unknown_account", `{"id":"gemini-web-unknown.json","label":"a@b.com"}`, false, 404, "account_not_found"},
		{"interrupted_session", `{"id":"ACCOUNT","label":"a@b.com"}`, true, 409, "label_requires_ready_session"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service, sidecar, record := localAccountFixture(t, scenario.interrupted)
			before := sidecar.count()
			expected := localReady
			if scenario.interrupted {
				expected = localRenewing
			}

			view, status := labelCall(t, service, strings.ReplaceAll(scenario.body, "ACCOUNT", record.ID))

			if status != scenario.status || view.Error != scenario.code {
				t.Fatalf("status=%d view=%+v want=%d/%s", status, view, scenario.status, scenario.code)
			}
			if sidecar.count() != before {
				t.Fatalf("unsafe rename reached credentials: calls=%d", sidecar.count()-before)
			}
			local, err := service.localStore().read(record.TokenRef)
			if err != nil || local.State != expected || local.Target.Label != record.Label {
				t.Fatalf("unsafe rename mutated record: state=%s label=%s err=%v", local.State, local.Target.Label, err)
			}
		})
	}
}
