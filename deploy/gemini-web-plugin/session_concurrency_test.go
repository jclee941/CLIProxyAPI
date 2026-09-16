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
	if sidecarPath(request) == gate.path && (gate.token == "" || request.Header.Get("x-goog-api-key") == gate.token) {
		gate.once.Do(func() { close(gate.entered); <-gate.release })
	}
	return gate.base.RoundTrip(request)
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
	switch sidecarPath(request) {
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
	if sidecarPath(request) == observer.failPath {
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
			service, host := loginFixture(t)
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
	service, host := loginFixture(t)
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
	if err != nil || local.Target.SessionRevision != 2 || local.Token != rotatedToken("test-login") {
		t.Fatal("disabled account lost committed renewal")
	}
}

func TestLoginCancellationPreventsCommit_whenIdentityInspectionIsProcessing(t *testing.T) {
	service, host := loginFixture(t)
	started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
	gate := &sessionWireGate{base: service.client.Transport, path: "/v1/session/inspect", entered: make(chan struct{}), release: make(chan struct{})}
	service.client.Transport = gate
	var release sync.Once
	unblock := func() { release.Do(func() { close(gate.release) }) }
	t.Cleanup(unblock)
	user := uint64(2)
	request := managementRequest{Method: "POST", Path: loginPath + "complete", Headers: http.Header{"Origin": {"https://manager.example"}}, HostCallbackID: "fixture-processing", Body: jsonFixture(t, loginCompletion{State: started.State, Token: encodedToken("test-login"), AccountSHA256: testAccountDigest, AuthUser: &user, ExtensionID: strings.Repeat("a", 32), Consent: true})}
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
	if status != 200 || view.Status != loginCancelled || host.saves != 0 {
		t.Fatal("cancelled handoff committed after inspection completed")
	}
}
