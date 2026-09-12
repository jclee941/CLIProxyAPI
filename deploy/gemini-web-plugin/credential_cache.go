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
	flight  *credentialFlight
}

type credentialFlight struct {
	done    chan struct{}
	epoch   uint64
	write   bool
	token   sessionToken
	err     error
	expires time.Time
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

func (service *service) resolveCredential(ctx context.Context, reference secretReference, fresh bool) (sessionToken, error) {
	lease := service.leases.get(reference.value)
	for {
		if err := ctx.Err(); err != nil {
			return sessionToken{}, err
		}
		lease.mu.Lock()
		if err := lease.credentialErrorLocked(service.now()); err != nil {
			lease.mu.Unlock()
			return sessionToken{}, err
		}
		if !fresh && lease.cache.token.value != "" && service.now().Before(lease.cache.expires) {
			token := lease.cache.token
			lease.mu.Unlock()
			return token, nil
		}
		if flight := lease.cache.flight; flight != nil {
			lease.mu.Unlock()
			select {
			case <-ctx.Done():
				return sessionToken{}, ctx.Err()
			case <-flight.done:
			}
			lease.mu.Lock()
			err := lease.credentialErrorLocked(service.now())
			if err == nil && lease.cache.epoch != flight.epoch {
				err = failure(409, "credential_changed")
			}
			lease.mu.Unlock()
			if err != nil {
				return sessionToken{}, err
			}
			if flight.err != nil {
				return sessionToken{}, flight.err
			}
			if fresh && flight.write || !service.now().Before(flight.expires) {
				continue
			}
			return flight.token, nil
		}
		flight := &credentialFlight{done: make(chan struct{}), epoch: lease.cache.epoch}
		lease.cache.flight = flight
		lease.mu.Unlock()

		token, err := service.secrets.Resolve(ctx, reference)

		lease.mu.Lock()
		if lease.cache.epoch != flight.epoch {
			token, err = sessionToken{}, failure(409, "credential_changed")
		}
		if blocked := lease.credentialErrorLocked(service.now()); blocked != nil {
			token, err = sessionToken{}, blocked
		}
		if lease.cache.epoch == flight.epoch {
			if err == nil {
				lease.cache.token, lease.cache.expires = token, service.now().Add(credentialCacheLifetime)
			} else {
				lease.cache.token, lease.cache.expires = sessionToken{}, time.Time{}
			}
		}
		flight.token, flight.err, flight.expires = token, err, lease.cache.expires
		lease.cache.flight = nil
		close(flight.done)
		lease.mu.Unlock()
		return token, err
	}
}

func (service *service) replaceCredential(ctx context.Context, request secretReplacement) error {
	lease := service.leases.get(request.Reference.value)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		lease.mu.Lock()
		if err := lease.credentialErrorLocked(service.now()); err != nil {
			lease.mu.Unlock()
			return err
		}
		if flight := lease.cache.flight; flight != nil {
			lease.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-flight.done:
				continue
			}
		}
		lease.clearCacheLocked()
		flight := &credentialFlight{done: make(chan struct{}), epoch: lease.cache.epoch, write: true}
		lease.cache.flight = flight
		lease.mu.Unlock()

		err := service.secrets.ReplaceIfExpected(ctx, request)

		lease.mu.Lock()
		if err == nil && lease.cache.epoch != flight.epoch {
			err = failure(503, "secret_write_outcome_unknown")
		}
		if err == nil {
			lease.cache.token, lease.cache.expires = request.Replacement, service.now().Add(credentialCacheLifetime)
			flight.token, flight.expires = lease.cache.token, lease.cache.expires
		} else if safeCredentialCode(err) == "secret_write_outcome_unknown" {
			lease.state = credentialState{state: maintenanceOperator, errCode: "secret_write_outcome_unknown"}
		}
		flight.err = err
		lease.cache.flight = nil
		close(flight.done)
		lease.mu.Unlock()
		return err
	}
}

func (service *service) putCredential(ctx context.Context, request secretWrite) (secretReference, error) {
	service.leases.mu.Lock()
	epoch := service.leases.epoch
	service.leases.mu.Unlock()
	reference, err := service.secrets.Put(ctx, request)
	if err != nil {
		return secretReference{}, err
	}
	service.leases.mu.Lock()
	defer service.leases.mu.Unlock()
	lease := service.leases.getLocked(reference.value)
	lease.mu.Lock()
	defer lease.mu.Unlock()
	lease.clearCacheLocked()
	if epoch != service.leases.epoch {
		lease.state = credentialState{state: maintenanceOperator, errCode: "secret_write_outcome_unknown"}
		return secretReference{}, failure(503, "secret_write_outcome_unknown")
	}
	lease.cache.token, lease.cache.expires = request.Token, service.now().Add(credentialCacheLifetime)
	return reference, nil
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
