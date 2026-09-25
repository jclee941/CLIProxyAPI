package main

import (
	"context"
	"errors"
	"os"
	"time"
)

// A Google web session dies from disuse: the rotating cookie has to be renewed
// on a cadence whether or not a request arrives. The executor only renews while
// serving a turn, so an idle account used to expire and need an operator login.
// This sweep keeps every ready session warm on its own.
const (
	keepAliveInterval = 8 * time.Minute
	keepAliveWorkers  = 4
	keepAliveAge      = 20 * time.Minute
)

// The caller holds the lifecycle lock while configuring sessions, so the
// closing signal is read inside the goroutine rather than at the call site.
// Rotation is off by default. Measured on 2026-09-16: sessions rotated by the
// sweep died with auth_error even when no restart cut them, so the sweep may
// only run where an operator has asked for it explicitly.
func (service *service) startKeepAlive() {
	if os.Getenv("GEMINI_WEB_KEEPALIVE") != "on" {
		return
	}
	go func() { service.keepSessionsAlive(service.lifecycle.closingSignal()) }()
}

func (service *service) keepSessionsAlive(closing <-chan struct{}) {
	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-closing:
			return
		case <-ticker.C:
			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				select {
				case <-closing:
					cancel()
				case <-ctx.Done():
				}
			}()
			service.refreshIdleSessions(ctx)
			cancel()
		}
	}
}

// refreshIdleSessions rotates every ready session once. It never touches the
// host projection: the stored reference and revision are unchanged, only the
// credential behind them is newer, so nothing downstream has to be told.
func (service *service) refreshIdleSessions(ctx context.Context) {
	store := service.localStore()
	if store == nil {
		return
	}
	records, err := store.records()
	if err != nil {
		return
	}
	work := make(chan localSession)
	done := make(chan struct{})
	workers := keepAliveWorkers
	if len(records) < workers {
		workers = len(records)
	}
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for local := range work {
				service.refreshSession(ctx, local)
			}
		}()
	}
	for _, local := range records {
		if local.State != localReady {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			close(work)
			for finished := 0; finished < workers; finished++ {
				<-done
			}
			return
		case work <- local:
		}
	}
	close(work)
	for finished := 0; finished < workers; finished++ {
		<-done
	}
}

func (service *service) refreshSession(ctx context.Context, local localSession) {
	// Google retires the old cookie the instant it issues a new one, so a
	// shutdown that cuts a rotation leaves nothing usable behind. Joining the
	// lifecycle makes the host drain this the way it drains a request.
	if err := service.lifecycle.enter(); err != nil {
		return
	}
	defer service.lifecycle.leave()
	reference := local.Target.TokenRef
	lease, err := service.acquireCredential(reference, true)
	if err != nil {
		return
	}
	defer lease.guard.Unlock()
	// Re-read under the lease: a turn may have moved the session meanwhile.
	current, err := service.localStore().read(reference)
	if err != nil || current.State != localReady || current.Token != local.Token {
		return
	}
	if service.now().Unix()-current.RotatedAt < int64(keepAliveAge/time.Second) {
		return
	}
	// Google invalidates the old cookie the moment it issues a new one, so the
	// intent is durable before the call: a process that dies mid-rotation leaves
	// renewal_intent, which the operator can see, instead of a stored cookie that
	// upstream has already retired.
	intent := current
	intent.State = localRenewing
	if err := service.localStore().write(intent); err != nil {
		return
	}
	renewed, identity, err := service.renewCredential(ctx, reference, sessionToken{current.Token})
	if err != nil {
		if restore := service.localStore().write(current); restore != nil {
			return
		}
		var authentication *AuthenticationFailure
		if errors.As(err, &authentication) {
			lease.set(credentialState{state: maintenanceCooldown, nextDue: service.now().Add(maintenanceAuthCooldown), errCode: safeCredentialCode(err)})
		}
		return
	}
	if identity != current.Identity {
		return
	}
	current.Token, current.RotatedAt = renewed.value, service.now().Unix()
	if err := service.localStore().write(current); err != nil {
		return
	}
	service.refreshQuota(ctx, current.Target, renewed)
	lease.set(credentialState{state: maintenanceReady, nextDue: service.now().Add(maintenanceReadyInterval)})
}
