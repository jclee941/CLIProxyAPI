package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"reflect"
)

func (service *service) completeLogin(ctx context.Context, callbackID string, body loginCompletion) (loginView, error) {
	flow, err := service.getLogin(body.State)
	if err != nil {
		return loginView{}, err
	}
	service.loginMu.Lock()
	flow = service.logins[body.State]
	if flow.View.Status != loginPending {
		service.loginMu.Unlock()
		if flow.Reference != "" {
			local, err := service.localStore().read(flow.Reference)
			if err != nil {
				return loginView{}, err
			}
			if local.LoginState != body.State || local.LoginTokenHash != tokenFingerprint(sessionToken{body.Token}) || local.Identity.AccountSHA256 != body.AccountSHA256 || local.Identity.AuthUser != *body.AuthUser {
				return loginView{}, failure(409, "login_replay_mismatch")
			}
		}
		return flow.View, nil
	}
	flow.View.Status = loginProcessing
	service.logins[body.State] = flow
	service.loginMu.Unlock()
	result, err := service.commitLogin(ctx, callbackID, loginCommit{Flow: flow, Body: body})
	if err != nil {
		flow.View.Status, flow.View.Error = loginError, safeCredentialCode(err)
		result = flow
	}
	service.loginMu.Lock()
	if current := service.logins[body.State]; current.View.Status == loginCancelled && current.Reference == "" {
		result = current
	}
	service.logins[body.State] = result
	service.loginMu.Unlock()
	return result.View, nil
}

type loginCommit struct {
	Flow loginFlow
	Body loginCompletion
}

func (service *service) commitLogin(ctx context.Context, callbackID string, commit loginCommit) (loginFlow, error) {
	flow, body := commit.Flow, commit.Body
	token, err := parseToken(body.Token)
	if err != nil {
		return flow, err
	}
	user, err := tokenAuthUser(token)
	if err != nil || user != *body.AuthUser {
		return flow, failure(409, "credential_identity_mismatch")
	}
	identity := credentialInspection{AccountSHA256: body.AccountSHA256, AuthUser: user}
	if flow.View.ExpectedIdentity != nil && *flow.View.ExpectedIdentity != identity {
		return flow, failure(409, "credential_identity_mismatch")
	}
	record := flow.Previous
	if record.ID == "" {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return flow, failure(500, "account_id_failed")
		}
		record = storageRecord{Type: provider, ID: "gemini-web-" + hex.EncodeToString(random[:]) + ".json"}
	}
	lease, err := service.accountLease(record)
	if err != nil {
		return flow, err
	}
	if !lease.guard.TryLock() {
		return flow, failure(409, "session_busy")
	}
	defer lease.guard.Unlock()
	if err := leaseBlockedForLogin(lease); err != nil {
		return flow, err
	}
	inspection, err := service.inspectCredential(ctx, "", token)
	if err != nil {
		return flow, err
	}
	if inspection != identity {
		return flow, failure(409, "credential_identity_mismatch")
	}
	account, err := service.accountModels(ctx, "", token)
	if err != nil {
		return flow, err
	}
	if account.AccountSHA256 != "" && account.AccountSHA256 != identity.AccountSHA256 {
		return flow, failure(409, "credential_identity_mismatch")
	}
	if flow.View.ExpiresAt <= service.now().Unix() {
		flow.View.Status = loginExpired
		return flow, nil
	}
	local := localSession{Identity: identity, Token: token.value, State: localHostPending, LoginState: flow.View.State, LoginExpires: flow.View.ExpiresAt, LoginTokenHash: tokenFingerprint(token)}
	if flow.Previous.ID != "" {
		latest, _, err := service.findRecord(callbackID, record.ID)
		if err != nil {
			return flow, err
		}
		if latest.TokenRef != record.TokenRef || latest.SessionRevision != record.SessionRevision {
			return flow, failure(409, "credential_changed")
		}
		trusted, err := service.loginIdentity(latest)
		if err != nil {
			return flow, err
		}
		if trusted != identity {
			return flow, failure(409, "binding_mismatch")
		}
		record, local.Previous = latest, latest
		// loginIdentity already refused anything but a local session reference.
		previous, err := service.localStore().read(record.TokenRef)
		if err != nil {
			return flow, err
		}
		if previous.State != localReady {
			return flow, failure(409, "session_requires_reconciliation")
		}
		local.LegacyRef, local.LegacyGUID, local.LegacyUserBound = previous.LegacyRef, previous.LegacyGUID, previous.LegacyUserBound
	}
	if !localReferencePattern.MatchString(record.TokenRef) {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return flow, failure(500, "session_reference_failed")
		}
		record.TokenRef = "session://gemini-web/" + hex.EncodeToString(random[:])
	}
	if record.SessionRevision == ^uint64(0) {
		return flow, failure(409, "session_revision_exhausted")
	}
	record.SessionRevision++
	record.Label = flow.Label
	local.Target = record
	auth, err := authFromRecord(record)
	if err != nil {
		return flow, err
	}
	local.Projection = string(auth.StorageJSON)
	store := service.localStore()
	service.loginMu.Lock()
	if current := service.logins[flow.View.State]; current.View.Status == loginCancelled {
		service.loginMu.Unlock()
		return current, nil
	}
	if flow.View.ExpiresAt <= service.now().Unix() {
		service.loginMu.Unlock()
		flow.View.Status = loginExpired
		return flow, nil
	}
	if err := store.write(local); err != nil {
		service.loginMu.Unlock()
		return flow, err
	}
	flow.Reference, flow.View.AccountID, flow.View.Status = record.TokenRef, record.ID, loginHostPending
	service.logins[flow.View.State] = flow
	service.loginMu.Unlock()
	if err := service.aliasAccountLease(record, lease); err != nil {
		flow.View.Error = safeCredentialCode(err)
		return flow, nil
	}
	lease.mu.Lock()
	if local.Previous.TokenRef != "" && local.Previous.TokenRef != record.TokenRef {
		if lease.retired == nil {
			lease.retired = make(map[string]bool)
		}
		lease.retired[local.Previous.TokenRef] = true
	}
	lease.state = credentialState{state: maintenanceHostPending}
	lease.mu.Unlock()
	view, err := service.syncLocalHost(ctx, callbackID, local)
	if err != nil {
		flow.View.Error = safeCredentialCode(err)
		return flow, nil
	}
	flow.View.Status, flow.View.ModelsReady, flow.View.Error = view.Status, view.ModelsReady, view.Error
	return flow, nil
}

func leaseBlockedForLogin(lease *credentialLease) error {
	switch lease.snapshot().state {
	case maintenanceOperator, maintenanceFenced, maintenanceHostPending:
		return failure(409, "session_requires_reconciliation")
	default:
		return nil
	}
}

func (service *service) reconcileLogin(ctx context.Context, callbackID string, flow loginFlow) (loginView, error) {
	local, err := service.localStore().read(flow.Reference)
	if err != nil {
		return loginView{}, err
	}
	if local.LoginState != flow.View.State {
		return loginView{}, failure(409, "login_superseded")
	}
	lease, err := service.accountLease(local.Target)
	if err != nil {
		return loginView{}, err
	}
	if !lease.guard.TryLock() {
		return loginView{}, failure(409, "session_busy")
	}
	defer lease.guard.Unlock()
	local, err = service.localStore().read(flow.Reference)
	if err != nil {
		return loginView{}, err
	}
	if local.LoginState != flow.View.State {
		return loginView{}, failure(409, "login_superseded")
	}
	view, err := service.syncLocalHost(ctx, callbackID, local)
	if err != nil {
		flow.View.Error, flow.View.ModelsReady, flow.View.Status = safeCredentialCode(err), false, loginHostPending
		if local.State == localRenewing || local.State == localSubmitting {
			flow.View.Status = loginError
		}
	} else {
		flow.View.Status, flow.View.ModelsReady, flow.View.Error = view.Status, view.ModelsReady, view.Error
	}
	service.loginMu.Lock()
	service.logins[flow.View.State] = flow
	service.loginMu.Unlock()
	return flow.View, nil
}

func (service *service) canonicalHostRecord(callbackID, id string) (storageRecord, bool, error) {
	entries, err := service.entries(callbackID)
	if err != nil {
		return storageRecord{}, false, err
	}
	for _, entry := range entries {
		if entry.ID != id {
			continue
		}
		record, enabled, err := service.getRecord(callbackID, entry)
		if err != nil {
			return record, false, err
		}
		var response struct {
			JSON json.RawMessage `json:"json"`
		}
		if err := service.callback("host.auth.get", callbackRequest{HostCallbackID: callbackID, AuthIndex: entry.AuthIndex}, &response); err != nil {
			return record, false, err
		}
		var stored struct {
			storageRecord
			RequestScopedErrors []stopRule `json:"request_scoped_errors"`
		}
		if strictJSON(response.JSON, &stored) != nil {
			return record, false, failure(409, "host_projection_invalid")
		}
		auth, err := authFromRecord(record)
		if err != nil {
			return record, false, err
		}
		if !reflect.DeepEqual(stored.RequestScopedErrors, auth.Metadata.RequestScopedErrors) {
			return record, false, failure(409, "host_projection_invalid")
		}
		stored.Disabled = record.Disabled
		if stored.storageRecord != record {
			return record, false, failure(409, "credential_changed")
		}
		return record, enabled, nil
	}
	return storageRecord{}, false, failure(404, "account_not_found")
}

func sameHostProjection(first, second storageRecord) bool {
	first.Disabled, second.Disabled = false, false
	return first == second
}

func (service *service) syncLocalHost(ctx context.Context, callbackID string, local localSession) (loginView, error) {
	view := loginView{Status: loginHostPending}
	if local.State != localHostPending && local.State != localReady {
		return view, failure(409, "needs_operator")
	}
	if err := service.checkLocalBinding(local.Target, local); err != nil {
		return view, err
	}
	current, _, err := service.findRecord(callbackID, local.Target.ID)
	if err != nil && safeCredentialCode(err) != "account_not_found" {
		return view, err
	}
	matched := err == nil && sameHostProjection(current, local.Target)
	canonicalMatched := matched
	if matched {
		_, _, canonicalErr := service.canonicalHostRecord(callbackID, local.Target.ID)
		if canonicalErr != nil && safeCredentialCode(canonicalErr) != "host_projection_invalid" {
			return view, canonicalErr
		}
		canonicalMatched = canonicalErr == nil
	}
	if !canonicalMatched {
		if err == nil && !matched && (!sameHostProjection(current, local.Previous) || current.Disabled != local.Target.Disabled) {
			return view, failure(409, "host_projection_conflict")
		}
		if local.State != localHostPending {
			local.State = localHostPending
			if err := service.localStore().write(local); err != nil {
				return view, err
			}
		}
		service.loginMu.Lock()
		if service.modelPending == nil {
			service.modelPending = make(map[string]uint64)
		}
		service.modelPending[local.Target.ID] = local.Target.SessionRevision
		service.loginMu.Unlock()
		var saved struct {
			Name string `json:"name"`
		}
		err = service.callback("host.auth.save", callbackRequest{HostCallbackID: callbackID, Name: local.Target.ID, JSON: json.RawMessage(local.Projection)}, &saved)
		service.loginMu.Lock()
		delete(service.modelPending, local.Target.ID)
		service.loginMu.Unlock()
		if err != nil {
			return view, failure(503, "host_sync_pending")
		}
	}
	canonical, enabled, err := service.canonicalHostRecord(callbackID, local.Target.ID)
	if err != nil {
		return view, err
	}
	if !sameHostProjection(canonical, local.Target) {
		return view, failure(409, "host_projection_conflict")
	}
	if local.State != localReady {
		local.State = localReady
		if err := service.localStore().write(local); err != nil {
			return view, err
		}
	}
	service.leases.get(local.Target.TokenRef).set(credentialState{state: maintenanceReady, nextDue: service.now().Add(maintenanceReadyInterval)})
	view.Status = loginSaved
	account, err := service.accountModels(ctx, local.Target.TokenRef, sessionToken{local.Token})
	if err != nil {
		view.Error = safeCredentialCode(err)
		return view, nil
	}
	if account.AccountSHA256 != "" && account.AccountSHA256 != local.Identity.AccountSHA256 {
		return view, failure(409, "credential_identity_mismatch")
	}
	for _, model := range verifiedModels(account) {
		if enabled && model.ID == flashModel {
			view.Status, view.ModelsReady = loginReady, true
		}
	}
	return view, nil
}
