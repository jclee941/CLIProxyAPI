package main

import (
	"net/http"
	"testing"
)

func TestKeepAliveRotatesIdleSession_withoutChangingTheHostProjection(t *testing.T) {
	service, record, saves := maintenanceFixture(t)
	original, err := service.localStore().read(record.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	localSidecarAll(t, service, func(writer http.ResponseWriter, request *http.Request) {
		switch sidecarPath(request) {
		case "/v1/session/renew":
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
			localSidecarAll(t, service, func(http.ResponseWriter, *http.Request) {
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
