package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type sessionWireGate struct {
	base    http.RoundTripper
	path    string
	token   string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (gate *sessionWireGate) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path == gate.path && (gate.token == "" || request.Header.Get("x-goog-api-key") == gate.token) {
		gate.once.Do(func() { close(gate.entered); <-gate.release })
	}
	return gate.base.RoundTrip(request)
}

func TestLoginMigrationRejectsLateLegacyModelPublication_whenModelInspectionWasInFlight(t *testing.T) {
	service, _, vault, record := boundLegacyFixture(t)
	old := sessionToken{encodedToken("test-older-model-session")}
	vault.tokens[record.TokenRef] = old
	gate := &sessionWireGate{base: service.client.Transport, path: "/v1/account-models", token: old.value, entered: make(chan struct{}), release: make(chan struct{})}
	service.client.Transport = gate
	var release sync.Once
	unblock := func() { release.Do(func() { close(gate.release) }) }
	t.Cleanup(unblock)
	finished := make(chan error, 1)
	raw := jsonFixture(t, struct {
		AuthID, AuthProvider string
		StorageJSON          []byte
	}{record.ID, provider, jsonFixture(t, record)})
	go func() { _, err := service.authOperation(context.Background(), "model.for_auth", raw); finished <- err }()
	<-gate.entered
	started, _ := loginCall(t, service, "start", jsonFixture(t, struct {
		Label      string `json:"label"`
		ExistingID string `json:"existing_id"`
		Consent    bool   `json:"consent"`
	}{"Relogin", record.ID, true}))
	ready := completeFixture(t, service, started)
	if ready.Status != loginReady {
		t.Fatalf("migration=%s error=%s", ready.Status, ready.Error)
	}

	unblock()
	err := <-finished

	if safeCredentialCode(err) != "credential_changed" {
		t.Fatalf("late legacy model publication accepted: %v", err)
	}
}

func boundLegacyFixture(t *testing.T) (*service, *loginHostFixture, *memorySecrets, storageRecord) {
	t.Helper()
	service, host, vault := loginFixture(t)
	record := recordFixture(t, "a")
	host.records[record.ID] = jsonFixture(t, record)
	user := uint64(2)
	service.config.MaintenanceSources = map[string]maintenanceSource{record.ID: {TokenRef: record.TokenRef, ProfileGUID: "00000000-0000-4000-8000-000000000001", ExpectedGaiaSHA256: strings.Repeat("b", 64), AuthUser: &user}}
	vault.tokens[record.TokenRef] = sessionToken{encodedToken("test-login")}
	return service, host, vault, record
}

func TestLoginMigrationSerializesWithGeneration_whenLegacyRequestIsInFlight(t *testing.T) {
	for _, model := range []string{flashModel, omniModel} {
		t.Run(model, func(t *testing.T) {
			service, host, _, record := boundLegacyFixture(t)
			path := "/v1/account-models"
			if model == omniModel {
				path = "/v1/session/renew"
			}
			gate := &sessionWireGate{base: service.client.Transport, path: path, entered: make(chan struct{}), release: make(chan struct{})}
			service.client.Transport = gate
			var release sync.Once
			unblock := func() { release.Do(func() { close(gate.release) }) }
			t.Cleanup(unblock)
			auth, err := authFromRecord(record)
			if err != nil {
				t.Fatal(err)
			}
			request := executorRequest{AuthID: record.ID, AuthProvider: provider, Model: model, Format: "gemini", SourceFormat: "gemini", Payload: []byte(`{"contents":[{"parts":[{"text":"fixture"}]}]}`), StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata}
			raw := jsonFixture(t, request)
			finished := make(chan []byte, 1)
			go func() { finished <- service.handle(context.Background(), "executor.execute", raw) }()
			<-gate.entered
			started, status := loginCall(t, service, "start", jsonFixture(t, struct {
				Label      string `json:"label"`
				ExistingID string `json:"existing_id"`
				Consent    bool   `json:"consent"`
			}{"Relogin", record.ID, true}))
			if status != 200 {
				t.Fatalf("start=%d", status)
			}

			view := completeFixture(t, service, started)

			if view.Status != loginError || view.Error != "session_busy" || host.saves != 0 {
				t.Fatalf("migration crossed generation lease: %+v", view)
			}
			unblock()
			<-finished
		})
	}
}

func TestLoginMigrationRejectsLateLegacyCachePublication_whenBackingReadWasAlreadyRunning(t *testing.T) {
	service, _, vault, record := boundLegacyFixture(t)
	paused := &pausedSecrets{memorySecrets: vault, entered: make(chan struct{}), release: make(chan struct{})}
	service.secrets = paused
	var release sync.Once
	unblock := func() { release.Do(func() { close(paused.release) }) }
	t.Cleanup(unblock)
	finished := make(chan error, 1)
	reference, err := parseReference(record.TokenRef, "homelab")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, err := service.resolveCredential(context.Background(), reference, false); finished <- err }()
	<-paused.entered
	started, _ := loginCall(t, service, "start", jsonFixture(t, struct {
		Label      string `json:"label"`
		ExistingID string `json:"existing_id"`
		Consent    bool   `json:"consent"`
	}{"Relogin", record.ID, true}))
	view := completeFixture(t, service, started)
	if view.Status != loginReady {
		t.Fatalf("migration=%s error=%s", view.Status, view.Error)
	}

	unblock()
	err = <-finished

	if safeCredentialCode(err) != "credential_changed" {
		t.Fatalf("late legacy credential published: %v", err)
	}
	if _, err := service.resolveCredential(t.Context(), reference, false); safeCredentialCode(err) != "credential_changed" {
		t.Fatal("retired reference resolved again")
	}
}

type sessionIntentObserver struct {
	base      http.RoundTripper
	store     *sessionStore
	reference string
	mu        sync.Mutex
	seen      []localState
	failPath  string
	failure   error
}

func (observer *sessionIntentObserver) RoundTrip(request *http.Request) (*http.Response, error) {
	var expected localState
	switch request.URL.Path {
	case "/v1/session/renew":
		expected = localRenewing
	case "/v1beta/models/gemini-web-omni:generateContent":
		expected = localSubmitting
	}
	if expected != "" {
		record, err := observer.store.read(observer.reference)
		if err != nil {
			return nil, err
		}
		if record.State != expected {
			return nil, failure(500, "durable_intent_missing")
		}
		observer.mu.Lock()
		observer.seen = append(observer.seen, expected)
		observer.mu.Unlock()
	}
	if request.URL.Path == observer.failPath {
		if observer.failure != nil {
			return nil, observer.failure
		}
		return &http.Response{StatusCode: 502, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"message":"caller_disconnected"}}`))}, nil
	}
	return observer.base.RoundTrip(request)
}

func TestLocalOmniPersistsIntentBeforeWireAndRetainsAmbiguity_whenUpstreamFails(t *testing.T) {
	for _, path := range []string{"/v1/session/renew", "/v1beta/models/gemini-web-omni:generateContent"} {
		t.Run(path, func(t *testing.T) {
			service, host, _ := loginFixture(t)
			started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
			ready := completeFixture(t, service, started)
			record, err := service.parseStorage(host.records[ready.AccountID], true)
			if err != nil {
				t.Fatal(err)
			}
			observer := &sessionIntentObserver{base: service.client.Transport, store: service.localStore(), reference: record.TokenRef, failPath: path}
			service.client.Transport = observer
			auth, err := authFromRecord(record)
			if err != nil {
				t.Fatal(err)
			}
			request := executorRequest{AuthID: record.ID, AuthProvider: provider, Model: omniModel, Format: "gemini", SourceFormat: "gemini", Payload: []byte(`{"contents":[{"parts":[{"text":"fixture"}]}]}`), StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, HostCallbackID: "fixture-omni"}

			result := invoke(t, service, "executor.execute", request)

			if result.OK {
				t.Fatal("ambiguous operation reported success")
			}
			local, err := service.localStore().read(record.TokenRef)
			if err != nil {
				t.Fatal(err)
			}
			expected := localRenewing
			if strings.Contains(path, "generateContent") {
				expected = localSubmitting
			}
			if local.State != expected {
				t.Fatalf("lost durable fence: %s", local.State)
			}
			observer.mu.Lock()
			calls := len(observer.seen)
			observer.mu.Unlock()
			request.StorageJSON = host.records[ready.AccountID]
			result = invoke(t, service, "executor.execute", request)
			observer.mu.Lock()
			defer observer.mu.Unlock()
			if result.OK || len(observer.seen) != calls {
				t.Fatal("ambiguous operation was automatically resubmitted")
			}
		})
	}
}

func TestLocalOmniStopsAfterDurableRenewal_whenHostDisablesAccountDuringSave(t *testing.T) {
	service, host, _ := loginFixture(t)
	started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
	ready := completeFixture(t, service, started)
	record, err := service.parseStorage(host.records[ready.AccountID], true)
	if err != nil {
		t.Fatal(err)
	}
	base := service.host
	service.host = func(method string, raw []byte) ([]byte, error) {
		if method == "host.auth.save" {
			host.mu.Lock()
			host.disabled = true
			host.mu.Unlock()
		}
		return base(method, raw)
	}
	observer := &sessionIntentObserver{base: service.client.Transport, store: service.localStore(), reference: record.TokenRef}
	service.client.Transport = observer
	auth, err := authFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}

	result := invoke(t, service, "executor.execute", executorRequest{AuthID: record.ID, AuthProvider: provider, Model: omniModel, Format: "gemini", SourceFormat: "gemini", Payload: []byte(`{"contents":[{"parts":[{"text":"fixture"}]}]}`), StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, HostCallbackID: "fixture-disabled"})

	if result.OK || result.Error.Code != "gemini_web_omni:account_disabled" {
		t.Fatalf("disabled account submitted: %+v", result.Error)
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.seen) != 1 || observer.seen[0] != localRenewing {
		t.Fatal("submission crossed host disable boundary")
	}
	local, err := service.localStore().read(record.TokenRef)
	if err != nil || local.Target.SessionRevision != 2 || local.Token != encodedToken("test-renewed") {
		t.Fatal("disabled account lost committed renewal")
	}
}

func TestLoginCancellationPreventsCommit_whenIdentityInspectionIsProcessing(t *testing.T) {
	service, host, vault := loginFixture(t)
	started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
	gate := &sessionWireGate{base: service.client.Transport, path: "/v1/session/inspect", entered: make(chan struct{}), release: make(chan struct{})}
	service.client.Transport = gate
	var release sync.Once
	unblock := func() { release.Do(func() { close(gate.release) }) }
	t.Cleanup(unblock)
	user := uint64(2)
	request := managementRequest{Method: "POST", Path: loginPath + "complete", Headers: http.Header{"Origin": {"https://manager.example"}}, HostCallbackID: "fixture-processing", Body: jsonFixture(t, loginCompletion{State: started.State, Token: encodedToken("test-login"), AccountSHA256: strings.Repeat("b", 64), AuthUser: &user, ExtensionID: strings.Repeat("a", 32), Consent: true})}
	raw := jsonFixture(t, request)
	finished := make(chan []byte, 1)
	go func() { finished <- service.handle(context.Background(), "management.handle", raw) }()
	<-gate.entered

	view, status := loginCall(t, service, "cancel", jsonFixture(t, struct {
		State string `json:"state"`
	}{started.State}))

	if status != 200 || view.Status != loginCancelled {
		t.Errorf("processing cancellation=%d %s", status, view.Status)
	}
	unblock()
	<-finished
	view, status = loginCall(t, service, "status", jsonFixture(t, struct {
		State string `json:"state"`
	}{started.State}))
	if status != 200 || view.Status != loginCancelled || host.saves != 0 || vault.writes != 0 {
		t.Fatal("cancelled handoff committed after inspection completed")
	}
}
