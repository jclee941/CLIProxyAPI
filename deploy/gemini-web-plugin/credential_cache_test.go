package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func cacheFixture(t *testing.T) (*service, *memorySecrets, secretReference) {
	t.Helper()
	service := newService(nil)
	reference, err := parseReference(recordFixture(t, "a").TokenRef, "homelab")
	if err != nil {
		t.Fatal(err)
	}
	store := &memorySecrets{tokens: map[string]sessionToken{reference.value: {encodedToken("synthetic-original")}}}
	service.secrets = store
	return service, store, reference
}

func TestCredentialCacheExpires_whenHardLifetimeReached(t *testing.T) {
	service, store, reference := cacheFixture(t)
	clock := time.Unix(100, 0)
	service.now = func() time.Time { return clock }
	original, err := service.resolveCredential(t.Context(), reference, false)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(59 * time.Minute)
	if token, err := service.resolveCredential(t.Context(), reference, false); err != nil || token != original {
		t.Fatal("unexpired token was not reused")
	}
	delete(store.tokens, reference.value)
	clock = clock.Add(time.Minute)

	token, err := service.resolveCredential(t.Context(), reference, false)

	if err == nil || token.value != "" || len(store.reads) != 2 {
		t.Fatal("expired token survived a failed backing lookup or hit extended its lifetime")
	}
}

func TestCredentialCacheIsolatesReferencesAndServices(t *testing.T) {
	instance, store, reference := cacheFixture(t)
	if _, err := instance.resolveCredential(t.Context(), reference, false); err != nil {
		t.Fatal(err)
	}
	other, err := parseReference("op://homelab/"+strings.Repeat("b", 26)+"/web-session", "homelab")
	if err != nil {
		t.Fatal(err)
	}
	second := newService(nil)
	second.secrets = store
	delete(store.tokens, reference.value)

	for _, target := range []struct {
		service   *service
		reference secretReference
	}{{instance, other}, {second, reference}} {
		if token, err := target.service.resolveCredential(t.Context(), target.reference, false); err == nil || token.value != "" {
			t.Fatal("credential crossed a reference or service boundary")
		}
	}
}

func TestCredentialCachePublishesOnlyCommittedReplacement(t *testing.T) {
	for _, outcome := range []string{"success", "credential_changed", "secret_store_unavailable", "secret_write_outcome_unknown"} {
		t.Run(outcome, func(t *testing.T) {
			service, memory, reference := cacheFixture(t)
			store := &renewalStore{memorySecrets: memorySecrets{tokens: memory.tokens}}
			service.secrets = store
			original, err := service.resolveCredential(t.Context(), reference, false)
			if err != nil {
				t.Fatal(err)
			}
			replacement := sessionToken{encodedToken("synthetic-replacement")}
			if outcome != "success" {
				store.writeErr = failure(503, outcome)
			}

			err = service.replaceCredential(t.Context(), secretReplacement{Reference: reference, Expected: original, Replacement: replacement})

			if (err == nil) != (outcome == "success") {
				t.Fatal("write outcome changed")
			}
			delete(store.tokens, reference.value)
			token, readErr := service.resolveCredential(t.Context(), reference, false)
			if outcome == "success" {
				if readErr != nil || token != replacement || len(store.reads) != 1 {
					t.Fatal("committed token was not published before further reads")
				}
			} else if readErr == nil || token.value != "" {
				t.Fatal("failed write retained a usable cached token")
			}
		})
	}
}

func TestCredentialCachePreservesReplacement_whenOldRequestIsRejected(t *testing.T) {
	service, store, reference := cacheFixture(t)
	original, err := service.resolveCredential(t.Context(), reference, false)
	if err != nil {
		t.Fatal(err)
	}
	replacement := sessionToken{encodedToken("synthetic-replacement")}
	if err := service.replaceCredential(t.Context(), secretReplacement{Reference: reference, Expected: original, Replacement: replacement}); err != nil {
		t.Fatal(err)
	}
	delete(store.tokens, reference.value)

	service.invalidateCredential(reference.value, original)
	token, err := service.resolveCredential(t.Context(), reference, false)

	if err != nil || token != replacement {
		t.Fatal("late rejection of the old token evicted the replacement")
	}
}

type blockingSecrets struct {
	memorySecrets
	started chan struct{}
	release chan struct{}
}

func (store *blockingSecrets) Resolve(ctx context.Context, reference secretReference) (sessionToken, error) {
	close(store.started)
	select {
	case <-ctx.Done():
		return sessionToken{}, ctx.Err()
	case <-store.release:
		return store.memorySecrets.Resolve(ctx, reference)
	}
}

func TestCredentialCacheDiscardsInflightRead_whenShutdownInvalidatesIt(t *testing.T) {
	service, memory, reference := cacheFixture(t)
	store := &blockingSecrets{memorySecrets: memorySecrets{tokens: memory.tokens}, started: make(chan struct{}), release: make(chan struct{})}
	service.secrets = store
	finished := make(chan error, 1)
	go func() {
		_, err := service.resolveCredential(t.Context(), reference, false)
		finished <- err
	}()
	<-store.started

	service.clearCredentials()
	close(store.release)

	if err := <-finished; safeCredentialCode(err) != "credential_changed" {
		t.Fatalf("late credential load survived invalidation: %v", err)
	}
}
