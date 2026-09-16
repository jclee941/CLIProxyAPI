package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

func parseCredentialReference(raw string) (string, error) {
	switch {
	case strings.HasPrefix(raw, "session://"):
		if _, err := sessionFile(raw); err != nil {
			return "", err
		}
		return raw, nil
	default:
		return "", failure(400, "invalid_token_reference")
	}
}

func (service *service) accountLease(record storageRecord) (*credentialLease, error) {
	service.leases.mu.Lock()
	defer service.leases.mu.Unlock()
	accountKey := "account:" + record.ID
	lease := service.leases.refs[accountKey]
	if lease == nil {
		if record.TokenRef != "" {
			lease = service.leases.getLocked(record.TokenRef)
		} else {
			lease = service.leases.getLocked(accountKey)
		}
	}
	if record.TokenRef != "" {
		if previous := service.leases.refs[record.TokenRef]; previous != nil && previous != lease {
			return nil, failure(409, "binding_mismatch")
		}
		service.leases.refs[record.TokenRef] = lease
	}
	return lease, nil
}

func (service *service) aliasAccountLease(record storageRecord, lease *credentialLease) error {
	service.leases.mu.Lock()
	defer service.leases.mu.Unlock()
	if previous := service.leases.refs[record.TokenRef]; previous != nil && previous != lease {
		return failure(409, "binding_mismatch")
	}
	service.leases.refs[record.TokenRef], service.leases.refs["account:"+record.ID] = lease, lease
	return nil
}

func (service *service) checkLocalBinding(record storageRecord, local localSession) error {
	if local.Target.ID != record.ID || local.Target.TokenRef != record.TokenRef {
		return failure(409, "credential_changed")
	}
	// A renewal moves the store forward and leaves the host one revision behind
	// until the sync lands, which is why the session parks in host_sync_pending
	// and keeps what the host still holds in Previous. Reading that gap as the
	// credential having changed refused every call made with the host's record
	// during the window a generation opens for itself, and an account that
	// answers nothing is dropped from the host's candidates - including when the
	// call was a caller retrieving the receipt that account is generating.
	if local.Target.SessionRevision != record.SessionRevision {
		if local.State != localHostPending || local.Previous.SessionRevision != record.SessionRevision ||
			local.Previous.ID != record.ID || local.Previous.TokenRef != record.TokenRef {
			return failure(409, "credential_changed")
		}
	}
	return nil
}

func (service *service) localRecord(record storageRecord) (localSession, error) {
	store := service.localStore()
	if store == nil {
		return localSession{}, failure(503, loginConfigurationError)
	}
	local, err := store.read(record.TokenRef)
	if err != nil {
		return localSession{}, err
	}
	if err := service.checkLocalBinding(record, local); err != nil {
		return localSession{}, err
	}
	if _, err := service.accountLease(record); err != nil {
		return localSession{}, err
	}
	return local, nil
}

func (service *service) rejectMigratedRecord(record storageRecord) error {
	store := service.localStore()
	if store == nil {
		return nil
	}
	records, err := store.records()
	if err != nil {
		return err
	}
	for _, local := range records {
		if local.Target.ID == record.ID {
			return failure(409, "credential_changed")
		}
	}
	return nil
}

func (service *service) resolveLocal(record storageRecord) (sessionToken, error) {
	local, err := service.localRecord(record)
	if err != nil {
		return sessionToken{}, err
	}
	switch local.State {
	case localReady:
		return sessionToken{local.Token}, nil
	case localHostPending:
		return sessionToken{}, failure(409, "host_sync_pending")
	case localRenewing, localSubmitting:
		return sessionToken{}, failure(409, "needs_operator")
	default:
		return sessionToken{}, failure(503, "session_store_corrupt")
	}
}

func (service *service) localModelSnapshot(record storageRecord) (localSession, error) {
	local, err := service.localRecord(record)
	if err != nil {
		return localSession{}, err
	}
	state := service.leases.get(record.TokenRef).snapshot()
	recoverable := local.State == localSubmitting && local.ContinuationActive != ""
	if state.state == maintenanceOperator && !recoverable || state.state == maintenanceFenced && service.now().Before(state.nextDue) {
		return localSession{}, failure(409, string(state.state))
	}
	switch local.State {
	case localReady, localHostPending:
		return local, nil
	case localSubmitting:
		if recoverable {
			return local, nil
		}
		return localSession{}, failure(409, "needs_operator")
	case localRenewing:
		return localSession{}, failure(409, "needs_operator")
	default:
		return localSession{}, failure(503, "session_store_corrupt")
	}
}

func (service *service) localAuthModels(ctx context.Context, record storageRecord) ([]modelInfo, error) {
	local, err := service.localModelSnapshot(record)
	if err != nil {
		return nil, err
	}
	account, err := service.accountModels(ctx, record.TokenRef, sessionToken{local.Token})
	if err != nil {
		return nil, err
	}
	latest, err := service.localModelSnapshot(record)
	if err != nil {
		return nil, err
	}
	// What must not change during the read is which credential answered: the same
	// account and the same auth user. Everything else this compared moves while
	// the account simply works. Google rotates the cookie on the very call that
	// reads the capabilities; a generation finishing turns submitting into ready
	// and clears the active turn key; and the renewal a generation runs first
	// parks the session in host_sync_pending and bumps the revision.
	//
	// Reading any of those as a swap published no models, and the host drops an
	// auth that publishes none. The account it dropped was the one holding the
	// receipt a caller was retrieving, so the retrieval was answered as though no
	// account existed while every account was healthy. Nothing is lost by
	// allowing them: the second snapshot goes through localModelSnapshot, which
	// already refuses renewing, an unrecoverable submission, operator and fence
	// outright, and the account digest is checked against this identity below.
	if latest.Identity != local.Identity {
		return nil, failure(409, "credential_changed")
	}
	if account.AccountSHA256 != "" && account.AccountSHA256 != local.Identity.AccountSHA256 {
		return nil, failure(409, "credential_identity_mismatch")
	}
	return service.interactionModels(account), nil
}

func (service *service) renewLocalSession(ctx context.Context, callbackID string, record storageRecord) (sessionToken, error) {
	local, err := service.localRecord(record)
	if err != nil {
		return sessionToken{}, err
	}
	if local.State != localReady {
		return sessionToken{}, failure(409, "session_requires_reconciliation")
	}
	latest, enabled, err := service.findRecord(callbackID, record.ID)
	if err != nil {
		return sessionToken{}, err
	}
	if !enabled {
		return sessionToken{}, failure(409, "account_disabled")
	}
	if !sameHostProjection(latest, record) {
		return sessionToken{}, failure(409, "credential_changed")
	}
	identity, err := service.inspectCredential(ctx, "", sessionToken{local.Token})
	if err != nil {
		return sessionToken{}, err
	}
	if identity != local.Identity {
		return sessionToken{}, failure(409, "credential_identity_mismatch")
	}
	local.State = localRenewing
	if err := service.localStore().write(local); err != nil {
		return sessionToken{}, err
	}
	token, identity, err := service.renewCredential(ctx, "", sessionToken{local.Token})
	if err != nil {
		return sessionToken{}, failure(409, "session_renewal_outcome_unknown_requires_operator")
	}
	if identity.AuthUser != local.Identity.AuthUser || identity.AccountSHA256 != local.Identity.AccountSHA256 {
		return sessionToken{}, failure(409, "session_renewal_identity_mismatch")
	}
	user, err := tokenAuthUser(token)
	if err != nil || user != local.Identity.AuthUser {
		return sessionToken{}, failure(409, "session_renewal_identity_mismatch")
	}
	if token.value == local.Token {
		local.State = localReady
		if err := service.localStore().write(local); err != nil {
			return sessionToken{}, err
		}
		latest, enabled, err := service.findRecord(callbackID, record.ID)
		if err != nil {
			return sessionToken{}, err
		}
		if !enabled {
			return sessionToken{}, failure(409, "account_disabled")
		}
		if !sameHostProjection(latest, record) {
			return sessionToken{}, failure(409, "credential_changed")
		}
		return token, nil
	}
	latest, enabled, err = service.findRecord(callbackID, record.ID)
	if err != nil {
		return sessionToken{}, err
	}
	if !sameHostProjection(latest, record) {
		return sessionToken{}, failure(409, "credential_changed")
	}
	local.Previous, local.Target, local.Token, local.State = latest, latest, token.value, localHostPending
	if local.Target.SessionRevision == ^uint64(0) {
		return sessionToken{}, failure(409, "session_revision_exhausted")
	}
	local.Target.SessionRevision++
	auth, err := authFromRecord(local.Target)
	if err != nil {
		return sessionToken{}, err
	}
	local.Projection = string(auth.StorageJSON)
	if err := service.localStore().write(local); err != nil {
		return sessionToken{}, err
	}
	if _, err := service.syncLocalHost(ctx, callbackID, local); err != nil {
		return sessionToken{}, err
	}
	latest, enabled, err = service.findRecord(callbackID, record.ID)
	if err != nil {
		return sessionToken{}, err
	}
	if !enabled {
		return sessionToken{}, failure(409, "account_disabled")
	}
	if !sameHostProjection(latest, local.Target) {
		return sessionToken{}, failure(409, "credential_changed")
	}
	return token, nil
}

func (service *service) localSubmission(record storageRecord, state localState) error {
	local, err := service.localStore().read(record.TokenRef)
	if err != nil {
		return err
	}
	if err := service.checkLocalBinding(local.Target, local); err != nil {
		return err
	}
	if state == localSubmitting && local.State != localReady || state == localReady && local.State != localSubmitting {
		return failure(409, "session_requires_reconciliation")
	}
	local.State = state
	return service.localStore().write(local)
}

func (service *service) inspectLocalAccount(ctx context.Context, record storageRecord) (sessionToken, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return sessionToken{}, failure(500, "auth_encoding_failed")
	}
	_, token, err := service.resolve(ctx, raw, record.ID)
	return token, err
}

func (service *service) maintainLocalAccount(ctx context.Context, target maintenanceTarget) maintenanceResult {
	result := maintenanceResult{ID: target.id, State: maintenanceCredentialError}
	record, enabled, err := service.findRecord(target.callbackID, target.id)
	if err != nil {
		result.Error = safeCredentialCode(err)
		return result
	}
	if !enabled {
		result.State = maintenanceDisabled
		return result
	}
	local, err := service.localStore().read(record.TokenRef)
	if err != nil {
		result.State, result.Error = maintenanceSourceRejected, safeCredentialCode(err)
		return result
	}
	if _, err := service.accountLease(local.Target); err != nil {
		result.Error = safeCredentialCode(err)
		return result
	}
	lease, err := service.acquireCredential(record.TokenRef, true)
	if err != nil {
		if safeCredentialCode(err) == "session_busy" {
			result.State = maintenanceBusy
		} else {
			result.State, result.Error = maintenanceOperator, safeCredentialCode(err)
		}
		return result
	}
	defer lease.guard.Unlock()
	state := lease.snapshot()
	if state.state == maintenanceCooldown && service.now().Before(state.nextDue) || !target.explicit && state.state == maintenanceReady && service.now().Before(state.nextDue) {
		return maintenanceStateResult(record.ID, state)
	}
	record, enabled, err = service.findRecord(target.callbackID, target.id)
	if err != nil {
		result.Error = safeCredentialCode(err)
		return result
	}
	if !enabled {
		result.State = maintenanceDisabled
		return result
	}
	local, err = service.localStore().read(record.TokenRef)
	if err != nil {
		result.Error = safeCredentialCode(err)
		return result
	}
	if err := service.checkLocalBinding(local.Target, local); err != nil {
		result.State, result.Error = maintenanceSourceRejected, safeCredentialCode(err)
		return result
	}
	if !sameHostProjection(record, local.Target) && !(local.State == localHostPending && sameHostProjection(record, local.Previous)) {
		result.State, result.Error = maintenanceSourceRejected, "credential_changed"
		return result
	}
	switch local.State {
	case localRenewing, localSubmitting:
		result.State, result.Error = maintenanceOperator, "session_operation_outcome_unknown"
		return result
	case localHostPending:
		_, err = service.syncLocalHost(ctx, target.callbackID, local)
		if err != nil {
			result.State, result.Error = maintenanceHostPending, safeCredentialCode(err)
			return result
		}
	case localReady:
		_, err = service.renewLocalSession(ctx, target.callbackID, record)
		if err != nil {
			var authentication *AuthenticationFailure
			if errors.As(err, &authentication) {
				lease.set(credentialState{state: maintenanceCooldown, nextDue: service.now().Add(maintenanceAuthCooldown), errCode: safeCredentialCode(err)})
				return maintenanceStateResult(record.ID, lease.snapshot())
			}
			latest, readErr := service.localStore().read(record.TokenRef)
			if readErr != nil {
				result.Error = safeCredentialCode(readErr)
				return result
			}
			switch latest.State {
			case localHostPending:
				result.State = maintenanceHostPending
			case localRenewing, localSubmitting:
				result.State = maintenanceOperator
			case localReady:
			}
			result.Error = safeCredentialCode(err)
			return result
		}
	default:
		result.Error = "session_store_corrupt"
		return result
	}
	lease.set(credentialState{state: maintenanceReady, nextDue: service.now().Add(maintenanceReadyInterval)})
	return maintenanceStateResult(record.ID, lease.snapshot())
}
