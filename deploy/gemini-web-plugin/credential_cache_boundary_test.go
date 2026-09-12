package main

import (
	"net/http"
	"testing"
	"time"
)

func TestCredentialCacheInvalidatesOnlyAuthenticationRejections(t *testing.T) {
	for _, status := range []int{401, 403, 429, 502} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			service, store, reference := cacheFixture(t)
			token, err := service.resolveCredential(t.Context(), reference, false)
			if err != nil {
				t.Fatal(err)
			}
			lease := service.leases.get(reference.value)
			lease.set(credentialState{state: maintenanceReady, nextDue: service.now().Add(time.Hour)})
			localSidecar(t, service, func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(status)
				writeFixture(t, writer, `{"error":{"message":"generic_rejection"}}`)
			})

			_, err = service.accountModels(t.Context(), reference.value, token)

			if err == nil {
				t.Fatal("upstream rejection disappeared")
			}
			delete(store.tokens, reference.value)
			_, cacheErr := service.resolveCredential(t.Context(), reference, false)
			if (cacheErr != nil) != (status == 401) {
				t.Fatalf("wrong cache invalidation for HTTP %d", status)
			}
			if lease.snapshot().nextDue.IsZero() != (status == 401) {
				t.Fatal("authentication rejection did not clear only the ready schedule")
			}
		})
	}
}

func TestCredentialCacheInvalidates_whenAccountIsUnavailable(t *testing.T) {
	service, store, reference := cacheFixture(t)
	token, err := service.resolveCredential(t.Context(), reference, false)
	if err != nil {
		t.Fatal(err)
	}
	localSidecar(t, service, func(writer http.ResponseWriter, _ *http.Request) {
		writeFixture(t, writer, `{"available":false,"models":[]}`)
	})

	_, err = service.accountModels(t.Context(), reference.value, token)

	if safeCredentialCode(err) != "account_unavailable" {
		t.Fatal("account rejection changed its public error")
	}
	delete(store.tokens, reference.value)
	if _, err := service.resolveCredential(t.Context(), reference, false); err == nil {
		t.Fatal("unavailable account retained a cached token")
	}
}

func TestCredentialCacheRejectsWarmRead_whenCredentialStateIsBlocked(t *testing.T) {
	for _, state := range []maintenanceState{maintenanceFenced, maintenanceOperator} {
		t.Run(string(state), func(t *testing.T) {
			service, store, reference := cacheFixture(t)
			if _, err := service.resolveCredential(t.Context(), reference, false); err != nil {
				t.Fatal(err)
			}
			service.leases.get(reference.value).set(credentialState{state: state, nextDue: service.now().Add(time.Minute)})

			_, err := service.resolveCredential(t.Context(), reference, false)

			if safeCredentialCode(err) != string(state) || len(store.reads) != 1 {
				t.Fatal("blocked credential reached a cache hit or backing lookup")
			}
		})
	}
}

func TestCredentialCachePreservesFreshReadBoundary(t *testing.T) {
	service, store, reference := cacheFixture(t)
	if _, err := service.resolveCredential(t.Context(), reference, false); err != nil {
		t.Fatal(err)
	}
	replacement := sessionToken{encodedToken("synthetic-external")}
	store.tokens[reference.value] = replacement

	token, err := service.resolveCredential(t.Context(), reference, true)

	if err != nil || token != replacement || len(store.reads) != 2 {
		t.Fatal("authoritative lookup returned a cached token")
	}
}

func TestCredentialCacheInvalidatesBindings_butNotDashboardChanges(t *testing.T) {
	service, store, record, _ := maintenanceFixture(t)
	reference, err := parseReference(record.TokenRef, "homelab")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.resolveCredential(t.Context(), reference, false); err != nil {
		t.Fatal(err)
	}
	config := service.settings()
	config.DashboardPath = "/tmp/other.html"
	service.mu.Lock()
	err = service.reconfigureCredentials(config)
	service.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	delete(store.tokens, reference.value)
	if _, err := service.resolveCredential(t.Context(), reference, false); err != nil {
		t.Fatal("dashboard-only change evicted a credential")
	}
	config.MaintenanceSources = nil

	service.mu.Lock()
	err = service.reconfigureCredentials(config)
	service.mu.Unlock()

	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.resolveCredential(t.Context(), reference, false); err == nil {
		t.Fatal("changed bindings retained a cached credential")
	}
}
