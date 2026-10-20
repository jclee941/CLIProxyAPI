package main

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestKeepAliveRotatesIdleSession_withoutChangingTheHostProjection(t *testing.T) {
	service, record, saves := maintenanceFixture(t)
	original, err := service.localStore().read(record.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	localWebAll(t, service, func(writer http.ResponseWriter, request *http.Request) {
		switch credentialRoute(request) {
		case "credential:renew":
			writeRotationFixture(writer, request)
		default:
			writeIdentityFixture(t, writer)
		}
	})

	service.refreshIdleSessions(t.Context())

	refreshed, err := service.localStore().read(record.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Token == original.Token {
		t.Fatal("idle session was not rotated")
	}
	if refreshed.Target != original.Target || refreshed.State != localReady {
		t.Fatalf("keep-alive moved the record: %+v", refreshed.Target)
	}
	if saves.Load() != 0 {
		t.Fatal("keep-alive resynchronised the host")
	}
}

func TestKeepAliveSkipsBusyAndInterruptedSessions(t *testing.T) {
	for _, scenario := range []string{"busy", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			service, record, _ := maintenanceFixture(t)
			before, err := service.localStore().read(record.TokenRef)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "busy" {
				lease, err := service.acquireCredential(record.TokenRef, true)
				if err != nil {
					t.Fatal(err)
				}
				defer lease.guard.Unlock()
			} else {
				interrupted := before
				interrupted.State = localSubmitting
				if err := service.localStore().write(interrupted); err != nil {
					t.Fatal(err)
				}
			}
			localWebAll(t, service, func(http.ResponseWriter, *http.Request) {
				t.Error("keep-alive touched a session it may not rotate")
			})

			service.refreshIdleSessions(t.Context())

			after, err := service.localStore().read(record.TokenRef)
			if err != nil || after.Token != before.Token {
				t.Fatalf("session was rotated: err=%v", err)
			}
		})
	}
}

func TestKeepAliveLeavesRenewalIntent_whenRotationNeverAnswers(t *testing.T) {
	service, record, _ := maintenanceFixture(t)
	localWebAll(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if credentialRoute(request) == "credential:renew" {
			rejectionFixture(t, writer, 500, `{"error":{"code":500,"message":"renew_unavailable"}}`)
			return
		}
		writeIdentityFixture(t, writer)
	})

	service.refreshIdleSessions(t.Context())

	after, err := service.localStore().read(record.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != localReady {
		t.Fatalf("an ambiguous rotation left the session unusable: %s", after.State)
	}
}

func TestKeepAliveSkipsASessionRotatedRecently(t *testing.T) {
	service, record, _ := maintenanceFixture(t)
	local, err := service.localStore().read(record.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	local.RotatedAt = service.now().Unix()
	if err := service.localStore().write(local); err != nil {
		t.Fatal(err)
	}
	localWebAll(t, service, func(http.ResponseWriter, *http.Request) { t.Error("a freshly rotated session was rotated again") })

	service.refreshIdleSessions(t.Context())
}

func TestKeepAliveIsDrainedByShutdown_soARestartCannotCutARotation(t *testing.T) {
	service, record, _ := maintenanceFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer close(release)
	localWebAll(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if credentialRoute(request) == "credential:renew" {
			once.Do(func() { close(entered) })
			<-release
			writeRotationFixture(writer, request)
			return
		}
		writeIdentityFixture(t, writer)
	})
	go service.refreshIdleSessions(context.Background())
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("rotation never reached the wire")
	}

	drained := make(chan struct{})
	go func() {
		_ = service.shutdownSessions()
		close(drained)
	}()

	select {
	case <-drained:
		t.Fatal("shutdown finished while a rotation was still in flight")
	case <-time.After(300 * time.Millisecond):
	}
	_ = record
}

func TestKeepAliveRotatesASessionOlderThanTheConfiguredAge(t *testing.T) {
	// Given: a session rotated two minutes ago.
	service, record, _ := maintenanceFixture(t)
	local, err := service.localStore().read(record.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	local.RotatedAt = service.now().Unix() - 120
	if err := service.localStore().write(local); err != nil {
		t.Fatal(err)
	}
	var renewals sync.WaitGroup
	renewed := 0
	localWebAll(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if credentialRoute(request) == "credential:renew" {
			renewed++
			writeRotationFixture(writer, request)
			return
		}
		writeIdentityFixture(t, writer)
	})
	renewals.Wait()

	// When: a sweep runs with the built-in age, then with a one-minute age.
	service.refreshIdleSessions(t.Context())
	before := renewed
	service.tuned.Store(&tuning{KeepAliveAgeSeconds: 60})
	service.refreshIdleSessions(t.Context())

	// Then: only the configured age let the sweep rotate it.
	refreshed, err := service.localStore().read(record.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if before != 0 || renewed != 1 || refreshed.Token == local.Token {
		t.Fatalf("renewals %d with the built-in age, %d in all, rotated %v", before, renewed, refreshed.Token != local.Token)
	}
}

func TestKeepAliveSweepsAtTheConfiguredInterval(t *testing.T) {
	// Given: a session due for rotation and a one-second sweep interval.
	service, _, _ := maintenanceFixture(t)
	service.tuned.Store(&tuning{KeepAliveIntervalSeconds: 1})
	rotated := make(chan struct{}, 1)
	localWebAll(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if credentialRoute(request) == "credential:renew" {
			writeRotationFixture(writer, request)
			select {
			case rotated <- struct{}{}:
			default:
			}
			return
		}
		writeIdentityFixture(t, writer)
	})
	closing := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		service.keepSessionsAlive(closing)
		close(stopped)
	}()
	defer func() {
		close(closing)
		<-stopped
	}()

	// When/Then: the sweep comes long before the built-in eight minutes.
	select {
	case <-rotated:
	case <-time.After(10 * time.Second):
		t.Fatal("no sweep ran at the configured interval")
	}
}
