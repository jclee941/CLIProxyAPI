package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func recoveryHost(t *testing.T, service *service, record storageRecord) {
	t.Helper()
	service.host = accountHost(t, []storageRecord{record})
}

func replaceAccount(body, id string) string {
	return strings.ReplaceAll(body, "ACCOUNT", id)
}

func recoverCall(t *testing.T, service *service, body string) (recoverView, int) {
	t.Helper()
	response, err := service.management(t.Context(), jsonFixture(t, managementRequest{Method: "POST", Path: recoverPath, HostCallbackID: "scope-list", Body: []byte(body)}))
	if err != nil {
		t.Fatal(err)
	}
	var view recoverView
	if err := json.Unmarshal(response.Body, &view); err != nil {
		t.Fatal(err)
	}
	return view, response.StatusCode
}

func TestOperatorRecoveryFinishesAPinnedTurn_whenTheReceiptIsLost(t *testing.T) {
	service, local := continuationFixture(t)
	continuationWeb(t, service, &continuationWebFixture{interrupted: true})
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first")))
	pinned, err := service.sessions.read(local.Target.TokenRef)
	if err != nil || pinned.ContinuationActive == "" {
		t.Fatalf("fixture did not pin a turn: %v", err)
	}
	recoveryHost(t, service, local.Target)

	view, status := recoverCall(t, service, `{"id":"`+local.Target.ID+`","consent":true}`)

	if status != 200 || !view.Released || view.State != string(localReady) {
		t.Fatalf("operator recovery did not release the account: status=%d view=%+v", status, view)
	}
}

func TestOperatorRecoveryRejectsUnsafeRequests(t *testing.T) {
	for _, scenario := range []struct {
		name, body string
		status     int
		code       string
	}{
		{"missing_consent", `{"id":"ACCOUNT"}`, 400, "invalid_recover_request"},
		{"denied_consent", `{"id":"ACCOUNT","consent":false}`, 400, "invalid_recover_request"},
		{"unknown_field", `{"id":"ACCOUNT","consent":true,"force":true}`, 400, "invalid_recover_request"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service, local := continuationFixture(t)
			recoveryHost(t, service, local.Target)

			view, status := recoverCall(t, service, replaceAccount(scenario.body, local.Target.ID))

			if status != scenario.status || view.Error != scenario.code {
				t.Fatalf("status=%d view=%+v", status, view)
			}
		})
	}
}

func TestOperatorRecoveryRefusesAnIdleAccount(t *testing.T) {
	service, local := continuationFixture(t)
	recoveryHost(t, service, local.Target)

	view, status := recoverCall(t, service, `{"id":"`+local.Target.ID+`","consent":true}`)

	if status != 409 || view.Error != "resolve_requires_interrupted_operation" {
		t.Fatalf("idle account recovered: status=%d view=%+v", status, view)
	}
}

func TestRecoveryRouteIsAdvertisedToTheHost(t *testing.T) {
	service := newService(nil)

	registered := invoke(t, service, "management.register", struct{}{})

	if !registered.OK {
		t.Fatalf("management register: %+v", registered.Error)
	}
	var management struct {
		Routes []struct{ Method, Path string }
	}
	if err := json.Unmarshal(registered.Result, &management); err != nil {
		t.Fatal(err)
	}
	for _, route := range management.Routes {
		if route.Method == "POST" && route.Path == "/plugins/gemini-web/recover" {
			return
		}
	}
	t.Fatalf("recovery route is not reachable through the host: %+v", management.Routes)
}

func TestCompanionExtensionIsServedFromTheDashboardDirectory(t *testing.T) {
	directory := t.TempDir()
	service := newService(nil)
	service.config.DashboardPath = filepath.Join(directory, "index.html")
	if err := os.WriteFile(filepath.Join(directory, extensionArchive), []byte("PK\x03\x04 fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	response, err := service.management(t.Context(), jsonFixture(t, managementRequest{Method: "GET", Path: extensionPath}))

	if err != nil || response.StatusCode != 200 || response.Headers.Get("Content-Type") != "application/zip" {
		t.Fatalf("extension not served: %v %+v", err, response.StatusCode)
	}
	if string(response.Body) != "PK\x03\x04 fixture" {
		t.Fatal("served a different artifact")
	}
}

func TestCompanionExtensionReportsUnavailable_whenItWasNotDeployed(t *testing.T) {
	service := newService(nil)
	service.config.DashboardPath = filepath.Join(t.TempDir(), "index.html")

	response, err := service.management(t.Context(), jsonFixture(t, managementRequest{Method: "GET", Path: extensionPath}))

	if err != nil || response.StatusCode != 503 {
		t.Fatalf("missing extension was not reported: %v %+v", err, response.StatusCode)
	}
}
