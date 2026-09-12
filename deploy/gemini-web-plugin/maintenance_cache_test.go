package main

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMaintenanceWaitsUntilDue_withoutResolvingOrRenewing(t *testing.T) {
	service, store, _, _ := maintenanceFixture(t)
	clock := time.Unix(100, 0)
	service.now = func() time.Time { return clock }
	var calls atomic.Int32
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path == "/v1/session/renew" {
			writeFixture(t, writer, `{"token":"`+encodedToken("original")+`","account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
			return
		}
		writeFixture(t, writer, `{"account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
	})
	if result := maintainFixture(t, service, `{}`); result.Results[0].State != maintenanceReady {
		t.Fatal("initial maintenance failed")
	}
	reads := len(store.reads)
	clock = clock.Add(29 * time.Minute)

	result := maintainFixture(t, service, `{}`)

	if result.Results[0].State != maintenanceReady || result.Results[0].NextDueAt == "" || len(store.reads) != reads || calls.Load() != 2 || store.writes != 0 {
		t.Fatal("scheduled ready account performed premature credential work")
	}
}

func TestMaintenanceRuns_whenDueOrExplicitlySelected(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "due", true: "explicit"}[explicit], func(t *testing.T) {
			service, _, record, _ := maintenanceFixture(t)
			clock := time.Unix(100, 0)
			service.now = func() time.Time { return clock }
			var renewals atomic.Int32
			localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/v1/session/renew" {
					renewals.Add(1)
					writeFixture(t, writer, `{"token":"`+encodedToken("original")+`","account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
					return
				}
				writeFixture(t, writer, `{"account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
			})
			maintainFixture(t, service, `{}`)
			body := `{}`
			if explicit {
				body = `{"id":"` + record.ID + `"}`
			} else {
				clock = clock.Add(30 * time.Minute)
			}

			result := maintainFixture(t, service, body)

			if result.Results[0].State != maintenanceReady || renewals.Load() != 2 {
				t.Fatal("due or explicitly selected maintenance did not run")
			}
		})
	}
}

func TestMaintenancePublishesRotationBeforeReadonlyHostCallback(t *testing.T) {
	service, store, record, _ := maintenanceFixture(t)
	reference, err := parseReference(record.TokenRef, "homelab")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.resolveCredential(t.Context(), reference, false); err != nil {
		t.Fatal(err)
	}
	originalHost := service.host
	service.host = func(method string, raw []byte) ([]byte, error) {
		if method == "host.auth.save" {
			token, err := service.resolveCredential(t.Context(), reference, false)
			if err != nil || token.value != encodedToken("renewed") {
				t.Error("host callback observed the previous token")
			}
		}
		return originalHost(method, raw)
	}
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/session/renew" {
			writeFixture(t, writer, `{"token":"`+encodedToken("renewed")+`","account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
			return
		}
		writeFixture(t, writer, `{"account_sha256":"`+strings.Repeat("a", 64)+`","auth_user":2}`)
	})

	result := maintainFixture(t, service, `{}`)

	if result.Results[0].State != maintenanceReady || len(store.reads) != 1 {
		t.Fatal("maintenance or its callback bypassed the cached credential boundary")
	}
}

func TestPendingMaintenanceReadsVault_whenCachedTokenMatchesPendingHash(t *testing.T) {
	service, store, record, _ := maintenanceFixture(t)
	reference, err := parseReference(record.TokenRef, "homelab")
	if err != nil {
		t.Fatal(err)
	}
	token, err := service.resolveCredential(t.Context(), reference, false)
	if err != nil {
		t.Fatal(err)
	}
	service.leases.get(reference.value).set(credentialState{state: maintenanceHostPending, tokenHash: tokenFingerprint(token)})
	store.tokens[reference.value] = sessionToken{encodedToken("synthetic-external-change")}
	localSidecar(t, service, func(http.ResponseWriter, *http.Request) {
		t.Error("changed pending credential reached Google")
	})

	result := maintainFixture(t, service, `{}`)

	if result.Results[0].State != maintenanceOperator || result.Results[0].Error != "credential_changed" || len(store.reads) != 2 {
		t.Fatal("pending reconciliation trusted its own cached token instead of the Vault")
	}
}
