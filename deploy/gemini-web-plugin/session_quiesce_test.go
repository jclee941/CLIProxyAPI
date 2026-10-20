package main

import (
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
