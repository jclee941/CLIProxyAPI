package main

import (
	"context"
	"time"
)

const credentialCacheLifetime = time.Hour

type credentialCache struct {
	token   sessionToken
	expires time.Time
	epoch   uint64
}

func (lease *credentialLease) credentialErrorLocked(now time.Time) error {
	if lease.state.state == maintenanceOperator || lease.state.state == maintenanceFenced && now.Before(lease.state.nextDue) {
		return failure(409, string(lease.state.state))
	}
	return nil
}

func (lease *credentialLease) clearCacheLocked() {
	lease.cache.token = sessionToken{}
	lease.cache.expires = time.Time{}
	lease.cache.epoch++
}

func (service *service) resolveCredential(ctx context.Context, reference string, fresh bool) (sessionToken, error) {
	if !localReferencePattern.MatchString(reference) {
		return sessionToken{}, failure(400, "invalid_token_reference")
	}
	store := service.localStore()
	if store == nil {
		return sessionToken{}, failure(503, loginConfigurationError)
	}
	local, err := store.read(reference)
	if err != nil {
		return sessionToken{}, err
	}
	return service.resolveLocal(local.Target)
}

func (service *service) invalidateCredential(reference string, token sessionToken) {
	if reference == "" {
		return
	}
	lease := service.leases.get(reference)
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.cache.token == token {
		lease.clearCacheLocked()
		if lease.state.state == maintenanceReady {
			lease.state.nextDue = time.Time{}
		}
	}
}

func (service *service) clearCredentials() {
	service.leases.mu.Lock()
	defer service.leases.mu.Unlock()
	service.leases.epoch++
	for _, lease := range service.leases.refs {
		lease.mu.Lock()
		lease.clearCacheLocked()
		lease.mu.Unlock()
	}
}
