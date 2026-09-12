package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

func TestCredentialCacheSharesColdFlight_whenConcurrentReadersArrive(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "unavailable"}[unavailable], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				service, _, reference := cacheFixture(t)
				release := make(chan struct{})
				var reads atomic.Int32
				service.secrets = opStore{vault: "homelab", run: func(ctx context.Context, _ []string, _ []byte) ([]byte, error) {
					reads.Add(1)
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-release:
					}
					if unavailable {
						return nil, failure(503, "secret_store_unavailable")
					}
					return []byte(encodedToken("synthetic-concurrent")), nil
				}}
				finished := make(chan error, 32)
				for range 32 {
					go func() {
						_, err := service.resolveCredential(t.Context(), reference, false)
						finished <- err
					}()
				}
				synctest.Wait()

				close(release)

				for range 32 {
					if err := <-finished; (err != nil) != unavailable {
						t.Errorf("shared flight outcome differs: %v", err)
					}
				}
				if reads.Load() != 1 {
					t.Fatalf("backing reads = %d, want 1", reads.Load())
				}
			})
		})
	}
}

func TestCredentialCacheWaiterCancels_withoutCancellingSharedLoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service, memory, reference := cacheFixture(t)
		store := &blockingSecrets{memorySecrets: memorySecrets{tokens: memory.tokens}, started: make(chan struct{}), release: make(chan struct{})}
		service.secrets = store
		leader := make(chan error, 1)
		go func() {
			_, err := service.resolveCredential(t.Context(), reference, false)
			leader <- err
		}()
		<-store.started
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		waiter := make(chan error, 1)
		go func() {
			_, err := service.resolveCredential(ctx, reference, false)
			waiter <- err
		}()
		synctest.Wait()

		cancel()

		if err := <-waiter; !errors.Is(err, context.Canceled) {
			t.Errorf("waiter cancellation lost: %v", err)
		}
		close(store.release)
		if err := <-leader; err != nil || len(store.reads) != 1 {
			t.Fatalf("waiter interrupted shared acquisition: %v", err)
		}
	})
}

func TestCredentialCacheSerializesReplacement_afterInflightLoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service, memory, reference := cacheFixture(t)
		original := memory.tokens[reference.value]
		replacement := sessionToken{encodedToken("synthetic-committed")}
		store := &blockingSecrets{memorySecrets: memorySecrets{tokens: memory.tokens}, started: make(chan struct{}), release: make(chan struct{})}
		service.secrets = store
		loaded := make(chan error, 1)
		go func() {
			_, err := service.resolveCredential(t.Context(), reference, false)
			loaded <- err
		}()
		<-store.started
		written := make(chan error, 1)
		go func() {
			written <- service.replaceCredential(t.Context(), secretReplacement{Reference: reference, Expected: original, Replacement: replacement})
		}()
		synctest.Wait()
		if store.writes != 0 {
			t.Error("write raced an unfinished backing read")
		}

		close(store.release)

		if err := <-loaded; err != nil {
			t.Fatal(err)
		}
		if err := <-written; err != nil {
			t.Fatal(err)
		}
		delete(store.tokens, reference.value)
		if token, err := service.resolveCredential(t.Context(), reference, false); err != nil || token != replacement {
			t.Fatal("late load replaced the committed token")
		}
	})
}
