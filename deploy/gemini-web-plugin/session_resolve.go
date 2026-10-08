package main

import (
	"context"
	"errors"
	"time"
)

// resolveIntent releases an interrupted renewal or submission only when a live
// identity probe of the stored credential answers definitively: an ambiguous
// probe leaves the record in its needs_operator state. It never resubmits a
// generation and never writes a credential.
func (service *service) resolveIntent(ctx context.Context, request managementRequest) (interface{}, error) {
	var body struct {
		ID      string `json:"id"`
		Consent bool   `json:"consent"`
	}
	if len(request.Body) > 40000 || strictJSON(request.Body, &body) != nil || !body.Consent {
		return nil, failure(400, "invalid_resolve_request")
	}
	record, enabled, err := service.findRecord(request.HostCallbackID, body.ID)
	if err != nil {
		return nil, err
	}
	state, credential, err := service.releaseInterruptedSession(ctx, record, false)
	if err != nil {
		return nil, err
	}
	return struct {
		ID         string `json:"id"`
		State      string `json:"state"`
		Credential string `json:"credential"`
		Enabled    bool   `json:"enabled"`
	}{record.ID, string(state), credential, enabled}, nil
}

// releaseInterruptedSession carries the release policy shared by the operator
// route and the automatic recovery that the account listing and the trapped
// session sweep run. An automatic release stamps the record so the dashboard
// can report when it happened.
func (service *service) releaseInterruptedSession(ctx context.Context, record storageRecord, automatic bool) (maintenanceState, string, error) {
	if !localReferencePattern.MatchString(record.TokenRef) {
		return "", "", failure(409, "local_session_required")
	}
	store := service.localStore()
	if store == nil {
		return "", "", failure(503, loginConfigurationError)
	}
	lease, err := service.accountLease(record)
	if err != nil {
		return "", "", err
	}
	if !lease.guard.TryLock() {
		return "", "", failure(409, "session_busy")
	}
	defer lease.guard.Unlock()
	local, err := store.read(record.TokenRef)
	if err != nil {
		return "", "", err
	}
	if err := service.checkLocalBinding(local.Target, local); err != nil {
		return "", "", err
	}
	switch local.State {
	case localRenewing, localSubmitting:
	default:
		return "", "", failure(409, "resolve_requires_interrupted_operation")
	}
	if !sameHostProjection(record, local.Target) {
		return "", "", failure(409, "credential_changed")
	}
	turns, err := continuationTurns(local)
	if err != nil {
		return "", "", err
	}
	// A pinned generation belongs to continuation recovery for as long as recovery
	// can still observe it upstream. A turn that never recorded an operation has
	// nothing left to observe, so recovery would answer outcome_unknown forever and
	// only the consenting operator route can end it. The automatic run never does.
	active := local.ContinuationActive
	observable := false
	if active != "" {
		turn, found := turns[active]
		observable = found && turn.Conversation != "" && turn.Reply != ""
		// A turn past the generation budget has already answered its caller,
		// whatever it answered, so nothing is waiting on it. Holding the account
		// for a recovery that can only confirm the same thing is how a restart
		// used to strand an account until someone noticed and clicked.
		if found && turn.StartedAt > 0 && service.now().Sub(time.Unix(turn.StartedAt, 0)) > webVideoBudget {
			observable = false
		}
		if automatic && observable {
			return "", "", failure(409, "continuation_recovery_required")
		}
	}
	identity, probe := service.inspectCredential(ctx, "", sessionToken{local.Token})
	var authentication *AuthenticationFailure
	rejected := errors.As(probe, &authentication)
	if probe != nil && !rejected {
		return "", "", failure(409, "session_outcome_still_unknown")
	}
	// Recovery reads the turn with this credential. Once the credential is
	// definitively rejected no recovery can ever observe it again, so deferring
	// would strand the account for good; a live credential still belongs to
	// recovery.
	if observable && !rejected {
		return "", "", failure(409, "continuation_recovery_required")
	}
	if probe == nil && identity != local.Identity {
		return "", "", failure(409, "credential_identity_mismatch")
	}
	local.State = localReady
	if automatic {
		local.AutoResolvedAt = service.now().Unix()
	}
	if active != "" {
		// Mark the ended turn so it is never mistaken for a completed one and can
		// never be chained from, exactly as a terminally video-less turn is.
		if turn, found := turns[active]; found {
			turn.State = "no_operation"
			turns[active] = turn
		}
		local.ContinuationActive = ""
		if err := service.saveContinuations(local, turns); err != nil {
			return "", "", err
		}
	} else if err := store.write(local); err != nil {
		return "", "", err
	}
	state, credential := maintenanceReady, "valid"
	if rejected {
		state, credential = maintenanceCooldown, safeCredentialCode(probe)
		lease.set(credentialState{state: state, nextDue: service.now().Add(maintenanceAuthCooldown), errCode: credential})
	} else {
		lease.set(credentialState{state: state, nextDue: service.now().Add(maintenanceReadyInterval)})
	}
	return state, credential, nil
}

// autoReleaseIfInterrupted recovers a record trapped by an interrupted omni
// submission or renewal under the same definitive-probe policy, and reports when
// the last automatic recovery happened. An ambiguous probe leaves it trapped.
func (service *service) autoReleaseIfInterrupted(ctx context.Context, record storageRecord) int64 {
	if !localReferencePattern.MatchString(record.TokenRef) {
		return 0
	}
	store := service.localStore()
	if store == nil {
		return 0
	}
	local, err := store.read(record.TokenRef)
	if err != nil {
		return 0
	}
	switch local.State {
	case localRenewing, localSubmitting:
	default:
		return local.AutoResolvedAt
	}
	if _, _, releaseErr := service.releaseInterruptedSession(ctx, record, true); releaseErr != nil {
		return local.AutoResolvedAt
	}
	released, err := store.read(record.TokenRef)
	if err != nil {
		return 0
	}
	return released.AutoResolvedAt
}

// trappedReleaseInterval paces the plugin's own run of the listing's automatic
// release. While the listing was its only trigger, an account trapped by an
// interrupted submission stayed out of every new room until someone opened the
// account listing.
const trappedReleaseInterval = 5 * time.Minute

// startTrappedSessionRelease runs whether or not the keep-alive sweep is on: a
// release probes a credential but never rotates it. The caller holds the
// lifecycle lock while configuring sessions, so the closing signal is read
// inside the goroutine.
func (service *service) startTrappedSessionRelease() {
	go func() { service.keepReleasingTrappedSessions(service.lifecycle.closingSignal()) }()
}

func (service *service) keepReleasingTrappedSessions(closing <-chan struct{}) {
	ticker := time.NewTicker(trappedReleaseInterval)
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
			service.releaseTrappedSessions(ctx)
			cancel()
		}
	}
}

// releaseTrappedSessions applies the listing's automatic release to every
// session left in an interrupted submission or renewal.
func (service *service) releaseTrappedSessions(ctx context.Context) {
	store := service.localStore()
	if store == nil {
		return
	}
	records, err := store.records()
	if err != nil {
		return
	}
	for _, local := range records {
		if ctx.Err() != nil {
			return
		}
		if local.State != localRenewing && local.State != localSubmitting {
			continue
		}
		service.releaseTrappedSession(ctx, local.Target)
	}
}

// releaseTrappedSession joins the lifecycle so a shutdown drains the release the
// way it drains a request.
func (service *service) releaseTrappedSession(ctx context.Context, record storageRecord) {
	if err := service.lifecycle.enter(); err != nil {
		return
	}
	defer service.lifecycle.leave()
	service.autoReleaseIfInterrupted(ctx, record)
}
