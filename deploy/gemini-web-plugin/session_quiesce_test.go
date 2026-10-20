package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func storeOwnerFree(t *testing.T, directory string) bool {
	t.Helper()
	store, err := openSessionStore(directory, sessionKeyFixture())
	var public *publicError
	if errors.As(err, &public) && public.Code == "session_store_already_owned" {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	return true
}

func TestQuiesceHandsTheStoreOwnerLockToAReplacement(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "sessions")
	svc := filesTestService(t, directory)
	if storeOwnerFree(t, directory) {
		t.Fatal("a replacement took the owner lock from a serving instance")
	}
	if reply := invoke(t, svc, "plugin.quiesce", nil); !reply.OK {
		t.Fatalf("quiesce: %+v", reply.Error)
	}
	if !storeOwnerFree(t, directory) {
		t.Fatal("the quiesced instance kept the owner lock")
	}
	if reply := invoke(t, svc, "quota.describe", nil); reply.OK || reply.Error.Code != "plugin_shutdown" {
		t.Fatalf("the quiesced instance answered a call: %+v", reply)
	}
}

func TestQuiesceKeepsTheStoreUntilRunningCallsReturn(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "sessions")
	svc := filesTestService(t, directory)
	if err := svc.lifecycle.enter(); err != nil {
		t.Fatal(err)
	}
	closing := svc.lifecycle.closingSignal()
	finished := make(chan []byte, 1)
	go func() { finished <- svc.handle(t.Context(), "plugin.quiesce", nil) }()
	select {
	case <-closing:
	case <-time.After(5 * time.Second):
		t.Fatal("the quiesce did not start")
	}
	if storeOwnerFree(t, directory) {
		t.Fatal("the quiesce released the store under a running call")
	}
	svc.lifecycle.leave()
	var reply envelope
	select {
	case raw := <-finished:
		if err := json.Unmarshal(raw, &reply); err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the quiesce did not finish once the call returned")
	}
	if !reply.OK {
		t.Fatalf("quiesce: %+v", reply.Error)
	}
	if !storeOwnerFree(t, directory) {
		t.Fatal("the quiesced instance kept the owner lock")
	}
}

func TestQuiesceGivesUpWhenRunningCallsOutlastTheDrainLimit(t *testing.T) {
	limit := quiesceDrainLimit
	quiesceDrainLimit = time.Millisecond
	t.Cleanup(func() { quiesceDrainLimit = limit })
	directory := filepath.Join(t.TempDir(), "sessions")
	svc := filesTestService(t, directory)
	if err := svc.lifecycle.enter(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.lifecycle.leave)
	if reply := invoke(t, svc, "plugin.quiesce", nil); reply.OK || reply.Error.Code != "quiesce_drain_timeout" {
		t.Fatalf("a quiesce over a running call: %+v", reply)
	}
	if storeOwnerFree(t, directory) {
		t.Fatal("an abandoned quiesce released the owner lock")
	}
	if reply := invoke(t, svc, "quota.describe", nil); !reply.OK {
		t.Fatalf("an abandoned quiesce left the instance refusing calls: %+v", reply.Error)
	}
}

func TestRegisterResumesAQuiescedInstanceTheHostKept(t *testing.T) {
	svc, _ := loginFixture(t)
	directory := svc.settings().SessionDir
	config, err := yaml.Marshal(svc.settings())
	if err != nil {
		t.Fatal(err)
	}
	if reply := invoke(t, svc, "plugin.quiesce", nil); !reply.OK {
		t.Fatalf("quiesce: %+v", reply.Error)
	}
	if !storeOwnerFree(t, directory) {
		t.Fatal("the quiesced instance kept the owner lock")
	}
	registered := invoke(t, svc, "plugin.register", struct {
		ConfigYAML []byte `json:"config_yaml"`
	}{config})
	if !registered.OK {
		t.Fatalf("register after the replacement failed: %+v", registered.Error)
	}
	if svc.localStore() == nil || storeOwnerFree(t, directory) {
		t.Fatal("the kept instance did not take its owner lock back")
	}
	if reply := invoke(t, svc, "quota.describe", nil); !reply.OK {
		t.Fatalf("the kept instance refused a call: %+v", reply.Error)
	}
}

func TestAQuiescedInstanceDefersTheHostRoutingHooks(t *testing.T) {
	svc := filesTestService(t, filepath.Join(t.TempDir(), "sessions"))
	if reply := invoke(t, svc, "plugin.quiesce", nil); !reply.OK {
		t.Fatalf("quiesce: %+v", reply.Error)
	}
	pick := invoke(t, svc, "scheduler.pick", map[string]any{"Model": "gemini-flash", "Candidates": []map[string]string{{"ID": "other", "Provider": "antigravity"}}})
	var picked continuationPick
	if !pick.OK || json.Unmarshal(pick.Result, &picked) != nil || picked.Handled {
		t.Fatalf("a quiesced instance refused or took a pick: %+v", pick)
	}
	intercepted := invoke(t, svc, "request.intercept_before", map[string]any{})
	var response requestInterceptResponse
	if !intercepted.OK || json.Unmarshal(intercepted.Result, &response) != nil || !slices.Contains(response.ClearHeaders, continuationHeader) {
		t.Fatalf("a quiesced instance did not strip the routing headers: %+v", intercepted)
	}
	if reply := invoke(t, svc, "quota.describe", nil); reply.OK {
		t.Fatal("a quiesced instance answered a call")
	}
}

func TestAQuiescedInstanceAnswersSessionExchangesBusy(t *testing.T) {
	// Given: an instance a hot reload has quiesced.
	svc := filesTestService(t, filepath.Join(t.TempDir(), "sessions"))
	if reply := invoke(t, svc, "plugin.quiesce", nil); !reply.OK {
		t.Fatalf("quiesce: %+v", reply.Error)
	}

	// When: Flow asks for a session exchange, and the account listing is asked for.
	exchange := invoke(t, svc, "management.handle", managementRequest{Method: "POST", Path: sessionExchangePath, Body: []byte(`{}`)})
	listing := invoke(t, svc, "management.handle", managementRequest{Method: "GET", Path: accountsPath})

	// Then: the exchange is refused as busy, which Flow waits out, and other
	// calls are refused as before.
	var response httpResponse
	var body struct {
		Error string `json:"error"`
	}
	if !exchange.OK || json.Unmarshal(exchange.Result, &response) != nil || json.Unmarshal(response.Body, &body) != nil || response.StatusCode != 409 || body.Error != "session_exchange_busy" {
		t.Fatalf("exchange answered %+v %d %s", exchange.Error, response.StatusCode, response.Body)
	}
	if listing.OK || listing.Error.Code != "plugin_shutdown" {
		t.Fatalf("listing answered %+v", listing)
	}
}

func TestQuiesceLetsARunningSessionExchangeFinish(t *testing.T) {
	// Given: a session exchange waiting for Flow's answer.
	fixture := newExchangeCancelFixture(t, false)
	request := sessionExchangeRequest{AuthID: fixture.record.ID, Method: "GET", URL: "https://flow.google.com/projects"}
	done := startExchangeManagement(t, fixture.service, managementRequest{
		Method: "POST", Path: sessionExchangePath, HostCallbackID: "scope-list", Body: jsonFixture(t, request),
	})
	pending := awaitExchangeSignal(t, fixture.started)

	// When: a hot reload quiesces the instance, then Flow answers.
	closing := fixture.service.lifecycle.closingSignal()
	quiesced := make(chan []byte, 1)
	go func() { quiesced <- fixture.service.handle(context.Background(), "plugin.quiesce", nil) }()
	awaitExchangeSignal(t, closing)
	close(pending.release)

	// Then: the exchange returned Flow's answer instead of being cancelled,
	// and the quiesce finished once it had.
	if response := exchangeManagementResponse(t, done); response.StatusCode != 200 {
		t.Fatalf("exchange = %d %s", response.StatusCode, response.Body)
	}
	var reply envelope
	if err := json.Unmarshal(awaitExchangeSignal(t, quiesced), &reply); err != nil || !reply.OK {
		t.Fatalf("quiesce: %v %+v", err, reply.Error)
	}
}
