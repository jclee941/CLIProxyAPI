package main

import (
	"context"
	"crypto/sha256"
	"errors"
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
	guard   sync.RWMutex
	mu      sync.Mutex
	state   credentialState
	retired map[string]bool
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
	if state.state == maintenanceFenced || state.state == maintenanceOperator {
	}
}

type credentialLeases struct {
	mu    sync.Mutex
	refs  map[string]*credentialLease
	epoch uint64
}

func (leases *credentialLeases) get(reference string) *credentialLease {
	leases.mu.Lock()
	defer leases.mu.Unlock()
	return leases.getLocked(reference)
}

func (leases *credentialLeases) getLocked(reference string) *credentialLease {
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

// A generation holds its account for minutes, so a caller that arrives during
// one is not wrong, it is early. Refusing it the instant the slot is taken is
// what made six linked accounts answer "no account": every one of them was
// working. These bound how long a turn waits for a slot, and they are variables
// so a test does not have to sit through the wait. Credential acquisition is the
// one place this plugin is allowed to wait on a clock.
var (
	credentialWaitAttempts = 45
	credentialWaitPoll     = 2 * time.Second
)

// waitForSlot takes the exclusive guard, waiting for a turn already in flight to
// end rather than reporting the account as unusable.
func waitForSlot(ctx context.Context, lease *credentialLease) bool {
	for attempt := 0; attempt < credentialWaitAttempts; attempt++ {
		if lease.guard.TryLock() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(credentialWaitPoll):
		}
	}
	return lease.guard.TryLock()
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

func (service *service) reconfigureCredentials(config pluginConfig) error { return nil }
func safeCredentialCode(err error) string {
	var public *publicError
	if errors.As(err, &public) {
		return public.Code
	}
	return "credential_operation_failed"
}

// safeCredentialMessage is safeCredentialCode plus anything the failure chose to
// say past its code, under the same rule that an error the plugin did not name
// is never quoted. A declined video turn is what this exists for: the code alone
// cannot tell a refused prompt from a spent daily allowance, and a subscriber
// reading the stream close deserves the same account the caller of a
// non-streaming turn already gets.
func safeCredentialMessage(err error) string {
	var public *publicError
	if errors.As(err, &public) {
		return public.Message
	}
	return "credential_operation_failed"
}

func (service *service) credentialFailure(reference string, err error) {
	lease := service.leases.get(reference)
	switch safeCredentialCode(err) {
	// A transport loss leaves the outcome unknown whichever path raised it, so
	// the native upkeep codes fence exactly like the bridge ones did.
	case "sidecar_transport_failed", "sidecar_response_failed",
		"web_transport_failed", "session_rotation_transport_failed":
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
