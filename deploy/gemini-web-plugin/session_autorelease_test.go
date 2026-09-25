package main

import (
	"encoding/json"
	"testing"
)

func listFirstAccount(t *testing.T, service *service) accountView {
	t.Helper()
	response, err := service.management(t.Context(), jsonFixture(t, managementRequest{Method: "GET", Path: accountsPath, HostCallbackID: "scope-resolve"}))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("listing failed: %d %s", response.StatusCode, response.Body)
	}
	var body struct {
		Accounts []accountView `json:"accounts"`
	}
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Accounts) != 1 {
		t.Fatalf("accounts=%d want 1", len(body.Accounts))
	}
	return body.Accounts[0]
}

func TestListingReleasesInterruptedSession_whenProbeIsDefinitive(t *testing.T) {
	service, _, record := localAccountFixture(t, true)

	view := listFirstAccount(t, service)

	if view.Status != "ready" {
		t.Fatalf("trapped account was not recovered: status=%s error=%s", view.Status, view.Error)
	}
	if view.AutoResolvedAt <= 0 {
		t.Fatal("automatic recovery was not recorded for the dashboard")
	}
	local, err := service.localStore().read(record.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if local.State != localReady {
		t.Fatalf("stored state=%s want %s", local.State, localReady)
	}
	if local.AutoResolvedAt <= 0 {
		t.Fatal("recovery timestamp was not persisted")
	}
}

func TestListingKeepsInterruptedSession_whenProbeIsAmbiguous(t *testing.T) {
	service, sidecar, record := localAccountFixture(t, true)
	sidecar.set("probe_unknown")

	view := listFirstAccount(t, service)

	if view.Status == "ready" {
		t.Fatal("an ambiguous probe released a trapped session")
	}
	if view.AutoResolvedAt != 0 {
		t.Fatalf("recorded a recovery that never happened: %v", view.AutoResolvedAt)
	}
	local, err := service.localStore().read(record.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if local.State != localRenewing {
		t.Fatalf("stored state=%s want %s", local.State, localRenewing)
	}
}

func TestListingReportsEarlierRecovery_whenAlreadyReleased(t *testing.T) {
	service, _, record := localAccountFixture(t, true)

	first := listFirstAccount(t, service)
	second := listFirstAccount(t, service)

	if first.AutoResolvedAt <= 0 || second.AutoResolvedAt != first.AutoResolvedAt {
		t.Fatalf("recovery record was not kept: first=%v second=%v", first.AutoResolvedAt, second.AutoResolvedAt)
	}
	local, err := service.localStore().read(record.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if local.State != localReady {
		t.Fatalf("stored state=%s want %s", local.State, localReady)
	}
}
