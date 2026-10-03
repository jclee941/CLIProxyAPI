package main

import (
	"net/http"
	"sync/atomic"
	"testing"
)

func rotationCountingSidecar(t *testing.T, service *service) *atomic.Int32 {
	t.Helper()
	rotations := &atomic.Int32{}
	localSidecarAll(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if sidecarPath(request) == "/v1/session/renew" {
			rotations.Add(1)
			writeRotationFixture(writer, request)
			return
		}
		writeIdentityFixture(t, writer)
	})
	return rotations
}

func readyLocalRecord(t *testing.T) (*service, *loginHostFixture, storageRecord) {
	t.Helper()
	service, host := loginFixture(t)
	started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
	ready := completeFixture(t, service, started)
	record, err := service.parseStorage(host.records[ready.AccountID], true)
	if err != nil {
		t.Fatal(err)
	}
	return service, host, record
}

func TestTurnRenewalSkipsTheRotation_whenTheSessionRotatedMomentsAgo(t *testing.T) {
	service, _, record := readyLocalRecord(t)
	local, err := service.localStore().read(record.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	local.RotatedAt = service.now().Unix() - 10
	if err := service.localStore().write(local); err != nil {
		t.Fatal(err)
	}
	rotations := rotationCountingSidecar(t, service)

	token, err := service.renewLocalSession(t.Context(), "fixture-renew", record)

	if err != nil {
		t.Fatalf("a fresh session failed its turn renewal: %v", err)
	}
	if rotations.Load() != 0 {
		t.Fatalf("a session rotated 10 seconds ago was rotated again %d time(s)", rotations.Load())
	}
	after, err := service.localStore().read(record.TokenRef)
	if err != nil || after.State != localReady || token.value != after.Token {
		t.Fatalf("state=%s err=%v token kept=%v", after.State, err, token.value == after.Token)
	}
}

func TestTurnRenewalRecordsItsRotation_soAnImmediateRetryReusesIt(t *testing.T) {
	service, host, record := readyLocalRecord(t)
	rotations := rotationCountingSidecar(t, service)

	first, err := service.renewLocalSession(t.Context(), "fixture-renew", record)
	if err != nil {
		t.Fatalf("first renewal: %v", err)
	}
	latest, err := service.parseStorage(host.records[record.ID], true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.renewLocalSession(t.Context(), "fixture-renew", latest)

	if err != nil {
		t.Fatalf("immediate retry failed its renewal: %v", err)
	}
	if rotations.Load() != 1 {
		t.Fatalf("rotations = %d, want the first turn's only", rotations.Load())
	}
	if second.value != first.value {
		t.Fatal("the retry did not reuse the session the first turn rotated")
	}
}
