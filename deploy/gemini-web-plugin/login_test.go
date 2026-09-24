package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLoginStartFailsActionably_whenStoreIsUnconfigured(t *testing.T) {
	service := newService(nil)
	request := managementRequest{Method: "POST", Path: "/v0/management/plugins/gemini-web/login/start", Headers: http.Header{"Origin": {"https://manager.example"}}, Body: []byte(`{"label":"Test","consent":true}`)}

	result := invoke(t, service, "management.handle", request)

	var response httpResponse
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 503 || body.Error != "login_requires_session_dir_manager_origin_extension_id_and_session_key" {
		t.Fatalf("status=%d error=%s", response.StatusCode, body.Error)
	}
}

type loginHostFixture struct {
	mu           sync.Mutex
	records      map[string]json.RawMessage
	service      *service
	saves        int
	loseResponse bool
	disabled     bool
	models       int
}

func (host *loginHostFixture) call(method string, raw []byte) ([]byte, error) {
	var request callbackRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	if request.HostCallbackID == "" {
		return nil, fmt.Errorf("missing callback scope")
	}
	host.mu.Lock()
	var result json.RawMessage
	var err error
	switch method {
	case "host.auth.list":
		entries := make([]hostEntry, 0, len(host.records))
		for id := range host.records {
			entries = append(entries, hostEntry{ID: id, Name: id, AuthIndex: id, Provider: provider, Disabled: host.disabled})
		}
		result, err = json.Marshal(struct {
			Files []hostEntry `json:"files"`
		}{entries})
	case "host.auth.get_runtime":
		result, err = json.Marshal(struct {
			Auth hostEntry `json:"auth"`
		}{hostEntry{ID: request.AuthIndex, Name: request.AuthIndex, AuthIndex: request.AuthIndex, Provider: provider, Disabled: host.disabled}})
	case "host.auth.get":
		result, err = json.Marshal(struct {
			JSON json.RawMessage `json:"json"`
		}{host.records[request.AuthIndex]})
	case "host.auth.save":
		host.saves++
		host.records[request.Name] = append(json.RawMessage(nil), request.JSON...)
		lost := host.loseResponse
		host.mu.Unlock()
		modelRequest, marshalErr := json.Marshal(struct {
			AuthID, AuthProvider string
			StorageJSON          []byte
		}{request.Name, provider, request.JSON})
		if marshalErr != nil {
			return nil, marshalErr
		}
		models, modelErr := host.service.authOperation(context.Background(), "model.for_auth", modelRequest)
		if modelErr != nil {
			return nil, modelErr
		}
		encoded, marshalErr := json.Marshal(models)
		if marshalErr != nil {
			return nil, marshalErr
		}
		var view struct{ Models []modelInfo }
		if err := json.Unmarshal(encoded, &view); err != nil {
			return nil, err
		}
		host.mu.Lock()
		host.models = len(view.Models)
		host.mu.Unlock()
		if lost {
			return nil, fmt.Errorf("fixture lost acknowledgement")
		}
		return []byte(`{"ok":true,"result":{"name":"saved"}}`), nil
	default:
		err = fmt.Errorf("unexpected callback %s", method)
	}
	host.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{OK: true, Result: result})
}

func loginFixture(t *testing.T) (*service, *loginHostFixture) {
	t.Helper()
	t.Setenv("GEMINI_WEB_SESSION_KEY", sessionKeyFixture())
	host := &loginHostFixture{records: make(map[string]json.RawMessage)}
	service := newService(host.call)
	host.service = service
	config := fmt.Sprintf("session_dir: %s\nmanager_origin: https://manager.example\nbrowser_extension_id: %s\n", filepath.Join(t.TempDir(), "sessions"), strings.Repeat("a", 32))
	registered := invoke(t, service, "plugin.register", struct {
		ConfigYAML []byte `json:"config_yaml"`
	}{[]byte(config)})
	if !registered.OK {
		t.Fatalf("register: %+v", registered.Error)
	}
	t.Cleanup(func() {
		if err := service.localStore().close(); err != nil {
			t.Error(err)
		}
	})
	localSidecarAll(t, service, func(writer http.ResponseWriter, request *http.Request) {
		switch sidecarPath(request) {
		case "/v1/session/inspect":
			writeIdentityFixture(t, writer)
		case "/v1/account-models":
			writeFixture(t, writer, `{"available":true,"observed_at":1234,"models":[{"capability_id":"actual","display_name":"3.8 Flash","mode":1}]}`)
		case "/v1/usage":
			writeFixture(t, writer, `{"tier":null,"metrics":null,"source":"GoogleWeb","estimated":false,"observed_at":1234}`)
		case "/v1/session/renew":
			writeRotationFixture(writer, request)
		case "/v1beta/models/gemini-3.8-flash:generateContent":
			writeFixture(t, writer, `{"candidates":[{"content":{"parts":[{"text":"fixture response"}]}}]}`)
		case "/v1beta/models/gemini-web-omni:generateContent":
			writeFixture(t, writer, `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"video/mp4","data":"Zml4dHVyZQ=="}}]}}]}`)
		default:
			t.Errorf("unexpected sidecar path %s", request.URL.Path)
			writer.WriteHeader(404)
		}
	})
	return service, host
}

func loginCall(t *testing.T, service *service, operation string, body []byte) (loginView, int) {
	t.Helper()
	result := invoke(t, service, "management.handle", managementRequest{Method: "POST", Path: loginPath + operation, HostCallbackID: "fixture-" + operation, Headers: http.Header{"Origin": {"https://manager.example"}}, Body: body})
	var response httpResponse
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	var view loginView
	if err := json.Unmarshal(response.Body, &view); err != nil {
		t.Fatal(err)
	}
	return view, response.StatusCode
}

func completeFixture(t *testing.T, service *service, started loginView) loginView {
	t.Helper()
	user := uint64(2)
	view, status := loginCall(t, service, "complete", jsonFixture(t, loginCompletion{State: started.State, Token: encodedToken("test-login"), AccountSHA256: testAccountDigest, AuthUser: &user, ExtensionID: strings.Repeat("a", 32), Consent: true}))
	if status != 200 {
		t.Fatalf("complete status=%d error=%s", status, view.Error)
	}
	return view
}

func TestLoginHandoffSavesAndExecutesWithoutVault_whenIdentityVerified(t *testing.T) {
	service, host := loginFixture(t)
	started, status := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
	if status != 200 || started.Status != loginPending || len(started.State) != 64 {
		t.Fatalf("start: %+v", started)
	}

	view := completeFixture(t, service, started)

	if view.Status != loginReady || !view.ModelsReady || host.saves != 1 || host.models != 1 {
		t.Fatalf("handoff status=%s saves=%d models=%d", view.Status, host.saves, host.models)
	}
	record, err := service.parseStorage(host.records[view.AccountID], true)
	if err != nil {
		t.Fatal(err)
	}
	if record.SessionRevision != 1 || !localReferencePattern.MatchString(record.TokenRef) || record.Disabled {
		t.Fatal("incorrect host projection")
	}
	for _, model := range []string{flashModel, omniModel} {
		auth, err := authFromRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		result := invoke(t, service, "executor.execute", executorRequest{AuthID: record.ID, AuthProvider: provider, Model: model, Format: "gemini", SourceFormat: "gemini", Payload: []byte(`{"contents":[{"parts":[{"text":"fixture"}]}]}`), StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, HostCallbackID: "fixture-execute"})
		if !result.OK {
			t.Fatalf("%s failed: %+v", model, result.Error)
		}
	}
}

func TestLoginReconcileDoesNotRepeatSave_whenAcknowledgementLost(t *testing.T) {
	service, host := loginFixture(t)
	host.loseResponse = true
	started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
	committed := completeFixture(t, service, started)
	if committed.Status != loginHostPending {
		t.Fatalf("expected pending, got %s", committed.Status)
	}

	view, status := loginCall(t, service, "reconcile", jsonFixture(t, struct {
		State string `json:"state"`
	}{started.State}))

	if status != 200 || view.Status != loginReady || host.saves != 1 {
		t.Fatalf("reconcile=%s saves=%d", view.Status, host.saves)
	}
	record, err := service.parseStorage(host.records[view.AccountID], true)
	if err != nil || record.SessionRevision != 1 {
		t.Fatal("retry advanced token revision")
	}
}

func TestLoginRejectsExpiredAndCancelledState_whenCompletionArrives(t *testing.T) {
	for _, operation := range []string{"expire", "cancel"} {
		t.Run(operation, func(t *testing.T) {
			service, host := loginFixture(t)
			now := time.Unix(1800000000, 0)
			service.now = func() time.Time { return now }
			started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
			if operation == "expire" {
				now = now.Add(10 * time.Minute)
			} else {
				loginCall(t, service, "cancel", jsonFixture(t, struct {
					State string `json:"state"`
				}{started.State}))
			}

			view := completeFixture(t, service, started)

			if view.Status != loginExpired && view.Status != loginCancelled {
				t.Fatalf("state=%s", view.Status)
			}
			if host.saves != 0 {
				t.Fatal("terminal state wrote credentials")
			}
		})
	}
}

func TestLoginRoutesAreManagementOnly_whenRegistered(t *testing.T) {
	service := newService(nil)

	result := invoke(t, service, "management.register", struct{}{})

	var registration struct {
		Routes    []struct{ Method, Path string } `json:"routes"`
		Resources []struct{ Path string }         `json:"resources"`
	}
	if err := json.Unmarshal(result.Result, &registration); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"start", "complete", "status", "cancel", "reconcile"} {
		found := false
		for _, route := range registration.Routes {
			found = found || route.Method == "POST" && route.Path == "/plugins/gemini-web/login/"+operation
		}
		if !found {
			t.Errorf("missing authenticated route %s", operation)
		}
	}
	for _, resource := range registration.Resources {
		if resource.Path != "/index" && resource.Path != "/extension" && resource.Path != "/openapi.json" {
			t.Fatalf("login introduced an unexpected resource %q", resource.Path)
		}
	}
}
