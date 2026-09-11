package main

import (
	"crypto/sha256"
	"errors"
	"reflect"
	"sync"
	"time"
)

type maintenanceState string

const (
	maintenanceReady           maintenanceState = "ready"
	maintenanceBusy            maintenanceState = "busy_skip"
	maintenanceDisabled        maintenanceState = "disabled_skip"
	maintenanceCooldown        maintenanceState = "cooldown"
	maintenanceSourceRejected  maintenanceState = "source_rejected"
	maintenanceCredentialError maintenanceState = "credential_error"
	maintenanceFenced          maintenanceState = "fenced"
	maintenanceHostPending     maintenanceState = "host_sync_pending"
	maintenanceOperator        maintenanceState = "needs_operator"
)

type credentialState struct {
	state      maintenanceState
	afterFence maintenanceState
	tokenHash  [32]byte
	nextDue    time.Time
	errCode    string
}

type credentialLease struct {
	guard sync.RWMutex
	mu    sync.Mutex
	state credentialState
}

func (lease *credentialLease) snapshot() credentialState {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	return lease.state
}

func (lease *credentialLease) set(state credentialState) {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	lease.state = state
}

type credentialLeases struct {
	mu   sync.Mutex
	refs map[string]*credentialLease
}

func (leases *credentialLeases) get(reference string) *credentialLease {
	leases.mu.Lock()
	defer leases.mu.Unlock()
	if leases.refs == nil {
		leases.refs = make(map[string]*credentialLease)
	}
	lease := leases.refs[reference]
	if lease == nil {
		lease = &credentialLease{}
		leases.refs[reference] = lease
	}
	return lease
}

func (service *service) acquireCredential(reference string, exclusive bool) (*credentialLease, error) {
	service.mu.RLock()
	defer service.mu.RUnlock()
	lease := service.leases.get(reference)
	var acquired bool
	if exclusive {
		acquired = lease.guard.TryLock()
	} else {
		acquired = lease.guard.TryRLock()
	}
	if !acquired {
		return nil, failure(409, "session_busy")
	}
	state := lease.snapshot()
	if state.state == maintenanceOperator || state.state == maintenanceFenced && service.now().Before(state.nextDue) {
		if exclusive {
			lease.guard.Unlock()
		} else {
			lease.guard.RUnlock()
		}
		return nil, failure(409, string(state.state))
	}
	if state.state == maintenanceFenced {
		resumed := state.afterFence
		if resumed == "" {
			resumed = maintenanceReady
		}
		lease.set(credentialState{state: resumed, tokenHash: state.tokenHash})
	}
	return lease, nil
}

func (service *service) reconfigureCredentials(config pluginConfig) error {
	if reflect.DeepEqual(config.MaintenanceSources, service.config.MaintenanceSources) {
		return nil
	}
	service.leases.mu.Lock()
	defer service.leases.mu.Unlock()
	locked := make([]*credentialLease, 0, len(service.leases.refs))
	defer func() {
		for _, lease := range locked {
			lease.guard.Unlock()
		}
	}()
	for _, lease := range service.leases.refs {
		if !lease.guard.TryLock() {
			return failure(409, "session_busy")
		}
		locked = append(locked, lease)
		state := lease.snapshot()
		if state.state == maintenanceOperator || state.state == maintenanceHostPending || state.state == maintenanceFenced && service.now().Before(state.nextDue) {
			return failure(409, "maintenance_state_requires_reconciliation")
		}
	}
	return nil
}

func safeCredentialCode(err error) string {
	var public *publicError
	if errors.As(err, &public) {
		return public.Code
	}
	return "credential_operation_failed"
}

func (service *service) credentialFailure(reference string, err error) {
	lease := service.leases.get(reference)
	switch safeCredentialCode(err) {
	case "sidecar_transport_failed", "sidecar_response_failed":
		state := lease.snapshot()
		if state.state != maintenanceFenced {
			state.afterFence = state.state
		}
		state.state, state.nextDue, state.errCode = maintenanceFenced, service.now().Add(credentialFenceDuration), "credential_transport_outcome_unknown"
		lease.set(state)
	case "secret_write_outcome_unknown":
		lease.set(credentialState{state: maintenanceOperator, errCode: "secret_write_outcome_unknown"})
	}
}

func tokenFingerprint(token sessionToken) [32]byte { return sha256.Sum256([]byte(token.value)) }
