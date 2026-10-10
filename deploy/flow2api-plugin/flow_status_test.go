package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFlowCreditsDoesNotMistakeTierForZeroBalance(t *testing.T) {
	for _, scenario := range []struct {
		raw   string
		value int
		valid bool
	}{
		{`[0,2,3,3]`, 0, true},
		{`[null,2,3,3]`, 0, true},
		{`[24867,2,3,3]`, 24867, true},
		{`["invalid",2,3,3]`, 0, false},
		{`[-1,2,3,3]`, -1, false},
		{`[]`, 0, false},
		{`{}`, 0, false},
	} {
		t.Run(scenario.raw, func(t *testing.T) {
			var payload any
			if err := json.Unmarshal([]byte(scenario.raw), &payload); err != nil {
				t.Fatal(err)
			}
			value, valid := flowCredits(payload)
			if value != scenario.value || valid != scenario.valid {
				t.Fatalf("balance = %d/%v, want %d/%v", value, valid, scenario.value, scenario.valid)
			}
		})
	}
}

func TestFlowStatusReportsLiveBalanceLabelAndObservation(t *testing.T) {
	fixture := newFlowFixture(t)
	service, record := flowService(t, fixture)
	service.now = func() time.Time { return time.Unix(1700000000, 0) }
	service.flowProjects = map[string]string{record.SourceAuthID: flowTestProject}
	service.host = func(method string, raw []byte) ([]byte, error) {
		var request struct {
			Callback string `json:"host_callback_id"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatal(err)
		}
		if method != "host.auth.list" || request.Callback != "fixture-scope" {
			t.Fatalf("metadata callback = %s %+v", method, request)
		}
		return jsonFixture(t, envelope{OK: true, Result: jsonFixture(t, map[string]any{
			"files": []any{map[string]any{"id": record.SourceAuthID, "name": "source-file", "label": "source-label"}},
		})}), nil
	}

	result, err := service.flowStatus(context.Background(), managementRequest{HostCallbackID: "fixture-scope"})
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Accounts []flowAccountView `json:"accounts"`
	}
	if err := json.Unmarshal(jsonFixture(t, result), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Accounts) != 1 {
		t.Fatalf("accounts = %+v", response.Accounts)
	}
	account := response.Accounts[0]
	if account.Label != "source-label" || account.Status != "ready" || account.Credits == nil ||
		*account.Credits != 142 || account.Tier == nil || *account.Tier != 3 ||
		account.ObservedAt != 1700000000 || account.Project != flowTestProject {
		t.Fatalf("observation = %+v", account)
	}
}

func TestFlowStatusReportsBusyLeaseWithoutWaiting(t *testing.T) {
	// Given: a Gemini generation holding the source account's lease.
	fixture := newFlowFixture(t)
	fixture.mu.Lock()
	fixture.busyRPC, fixture.busyRemaining = "nzlxg", 1
	fixture.mu.Unlock()
	service, _ := flowService(t, fixture)
	service.flowWait = func(context.Context, time.Duration) error {
		t.Error("the account listing waited for the lease")
		return nil
	}

	// When: the accounts are listed.
	result, err := service.flowStatus(context.Background(), managementRequest{})
	if err != nil {
		t.Fatal(err)
	}

	// Then: the account shows busy at once, with no balance.
	var response struct {
		Accounts []flowAccountView `json:"accounts"`
	}
	if err := json.Unmarshal(jsonFixture(t, result), &response); err != nil {
		t.Fatal(err)
	}
	if account := response.Accounts[0]; account.Status != "busy" || account.Credits != nil {
		t.Fatalf("busy lease listed as %+v", account)
	}
}

func TestFlowStatusKeepsFailedCreditLookupUnknown(t *testing.T) {
	fixture := newFlowFixture(t)
	fixture.mu.Lock()
	fixture.replies["nzlxg"] = []string{flowErrorEnvelope(t, []any{"er", nil, nil, nil, nil, 401, "generic"})}
	fixture.mu.Unlock()
	service, _ := flowService(t, fixture)
	result, err := service.flowStatus(context.Background(), managementRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Accounts []flowAccountView `json:"accounts"`
	}
	if err := json.Unmarshal(jsonFixture(t, result), &response); err != nil {
		t.Fatal(err)
	}
	account := response.Accounts[0]
	if account.Status != "expired" || account.Credits != nil || account.Tier != nil || account.ObservedAt != 0 || account.Error != "flow_unauthenticated" {
		t.Fatalf("failed observation became a balance: %+v", account)
	}
}

func TestFlowDashboardResourceServesTheDeployedArtifact(t *testing.T) {
	service := newService(nil)
	service.dashboard = filepath.Join(t.TempDir(), "index.html")
	html := []byte("<!doctype html><title>fixture</title>")
	if err := os.WriteFile(service.dashboard, html, 0600); err != nil {
		t.Fatal(err)
	}
	result := invoke(t, service, "management.handle", managementRequest{Method: "GET", Path: flowResourcePath})
	if !result.OK {
		t.Fatalf("resource failed: %+v", result.Error)
	}
	var response httpResponse
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(response.Body) != string(html) ||
		response.Headers.Get("Content-Type") != "text/html; charset=utf-8" || response.Headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("resource = %+v", response)
	}
}

func TestFlowDashboardRegistrationUsesExistingPluginMenu(t *testing.T) {
	service := newService(nil)
	result := invoke(t, service, "management.register", map[string]any{})
	var registration struct {
		Resources []struct{ Path string } `json:"resources"`
	}
	if err := json.Unmarshal(result.Result, &registration); err != nil {
		t.Fatal(err)
	}
	if !result.OK || len(registration.Resources) != 1 || registration.Resources[0].Path != "/index" {
		t.Fatalf("resource registration = %+v", registration)
	}
}
