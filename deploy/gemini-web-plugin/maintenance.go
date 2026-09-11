package main

import (
	"context"
	"errors"
	"sort"
	"time"
)

type maintenanceResult struct {
	ID        string           `json:"id"`
	State     maintenanceState `json:"state"`
	Error     string           `json:"error,omitempty"`
	NextDueAt string           `json:"next_due_at,omitempty"`
}

type maintenanceResponse struct {
	Results []maintenanceResult `json:"results"`
}

func (service *service) maintain(ctx context.Context, request managementRequest) (maintenanceResponse, error) {
	var body struct {
		ID *string `json:"id"`
	}
	if len(request.Body) > 4096 || strictJSON(request.Body, &body) != nil {
		return maintenanceResponse{}, failure(400, "invalid_maintenance_request")
	}
	selectedID := ""
	if body.ID != nil {
		if !accountIDPattern.MatchString(*body.ID) {
			return maintenanceResponse{}, failure(400, "invalid_account_id")
		}
		selectedID = *body.ID
	}
	bindings := service.settings().MaintenanceSources
	if selectedID != "" {
		if _, exists := bindings[selectedID]; !exists {
			return maintenanceResponse{}, failure(404, "maintenance_binding_not_found")
		}
	}
	entries, err := service.entries(request.HostCallbackID)
	if err != nil {
		return maintenanceResponse{}, err
	}
	registered := make(map[string]bool, len(entries))
	for _, entry := range entries {
		registered[entry.ID] = true
	}
	ids := make([]string, 0, len(bindings))
	for id := range bindings {
		if registered[id] && (selectedID == "" || selectedID == id) {
			ids = append(ids, id)
		}
	}
	if selectedID != "" && len(ids) == 0 {
		return maintenanceResponse{}, failure(404, "account_not_found")
	}
	sort.Strings(ids)
	result := maintenanceResponse{Results: make([]maintenanceResult, 0, len(ids))}
	for _, id := range ids {
		result.Results = append(result.Results, service.maintainAccount(ctx, request.HostCallbackID, id))
	}
	return result, nil
}

func (service *service) maintainAccount(ctx context.Context, callbackID, id string) maintenanceResult {
	binding, exists := service.settings().MaintenanceSources[id]
	if !exists {
		return maintenanceResult{ID: id, State: maintenanceSourceRejected, Error: "binding_mismatch"}
	}
	lease, err := service.acquireCredential(binding.TokenRef, true)
	if err != nil {
		state := service.leases.get(binding.TokenRef).snapshot()
		if safeCredentialCode(err) == "session_busy" {
			return maintenanceResult{ID: id, State: maintenanceBusy}
		}
		return maintenanceStateResult(id, state)
	}
	defer lease.guard.Unlock()
	current, exists := service.settings().MaintenanceSources[id]
	if !exists || current != binding {
		return maintenanceResult{ID: id, State: maintenanceSourceRejected, Error: "binding_mismatch"}
	}
	record, enabled, err := service.findRecord(callbackID, id)
	if err != nil {
		return maintenanceResult{ID: id, State: maintenanceCredentialError, Error: safeCredentialCode(err)}
	}
	if !enabled {
		return maintenanceResult{ID: id, State: maintenanceDisabled}
	}
	if record.TokenRef != binding.TokenRef {
		return maintenanceResult{ID: id, State: maintenanceSourceRejected, Error: "binding_mismatch"}
	}
	reference, err := parseReference(record.TokenRef, service.settings().Vault)
	if err != nil {
		return maintenanceResult{ID: id, State: maintenanceSourceRejected, Error: safeCredentialCode(err)}
	}
	token, err := service.secrets.Resolve(ctx, reference)
	if err != nil {
		return maintenanceResult{ID: id, State: maintenanceCredentialError, Error: safeCredentialCode(err)}
	}
	state := lease.snapshot()
	if state.state == maintenanceHostPending && state.tokenHash != tokenFingerprint(token) {
		lease.set(credentialState{state: maintenanceOperator, errCode: "credential_changed"})
		return maintenanceStateResult(id, lease.snapshot())
	}
	if (state.state == maintenanceCooldown || state.state == maintenanceHostPending) && state.tokenHash == tokenFingerprint(token) && service.now().Before(state.nextDue) {
		result := maintenanceStateResult(id, state)
		result.State = maintenanceCooldown
		return result
	}
	authUser, err := tokenAuthUser(token)
	if err != nil {
		return maintenanceResult{ID: id, State: maintenanceCredentialError, Error: safeCredentialCode(err)}
	}
	if binding.AuthUser != nil && *binding.AuthUser != authUser {
		return service.rejectMaintenance(id, token, failure(409, "binding_mismatch"))
	}
	inspection, err := service.inspectCredential(ctx, record.TokenRef, token)
	if state.state == maintenanceHostPending {
		if err == nil && (inspection.AccountSHA256 != binding.ExpectedGaiaSHA256 || inspection.AuthUser != authUser) {
			err = failure(409, "credential_identity_mismatch")
		}
		if err != nil {
			if fenced := lease.snapshot(); fenced.state == maintenanceFenced {
				return maintenanceStateResult(id, fenced)
			}
			state.errCode = safeCredentialCode(err)
			var authentication *AuthenticationFailure
			if errors.As(err, &authentication) || state.errCode == "credential_identity_mismatch" || state.errCode == "credential_identity_invalid" {
				state.nextDue = service.now().Add(maintenanceAuthCooldown)
			}
			lease.set(state)
			return maintenanceStateResult(id, state)
		}
		if err := service.syncCredentialHost(callbackID, record); err != nil {
			return maintenanceResult{ID: id, State: maintenanceHostPending, Error: safeCredentialCode(err)}
		}
		lease.set(credentialState{state: maintenanceReady})
		return maintenanceResult{ID: id, State: maintenanceReady}
	}
	if err == nil && (inspection.AccountSHA256 != binding.ExpectedGaiaSHA256 || inspection.AuthUser != authUser) {
		return service.rejectMaintenance(id, token, failure(409, "credential_identity_mismatch"))
	}
	renewed := token
	if err == nil {
		renewed, err = service.renewSession(ctx, record, token)
	}
	recovered := false
	var authentication *AuthenticationFailure
	if errors.As(err, &authentication) {
		captured, captureErr := service.source.Capture(ctx, captureRequest{Binding: binding, Bindings: service.settings().MaintenanceSources, AuthUser: authUser})
		if captureErr != nil {
			return service.rejectMaintenance(id, token, captureErr)
		}
		capturedUser, tokenErr := tokenAuthUser(captured.Token)
		if tokenErr != nil || captured.AccountSHA256 != binding.ExpectedGaiaSHA256 || capturedUser != authUser {
			return service.rejectMaintenance(id, token, failure(409, "source_identity_mismatch"))
		}
		verified, inspectErr := service.inspectCredential(ctx, record.TokenRef, captured.Token)
		if inspectErr != nil {
			return service.failedMaintenance(id, token, inspectErr)
		}
		if verified.AccountSHA256 != binding.ExpectedGaiaSHA256 || verified.AuthUser != authUser {
			return service.rejectMaintenance(id, token, failure(409, "source_identity_mismatch"))
		}
		renewed, recovered, err = captured.Token, true, nil
	}
	if err != nil {
		return service.failedMaintenance(id, token, err)
	}
	latest, enabled, err := service.findRecord(callbackID, id)
	if err != nil {
		return service.failedMaintenance(id, token, err)
	}
	if !enabled {
		return maintenanceResult{ID: id, State: maintenanceDisabled}
	}
	if latest.TokenRef != record.TokenRef {
		return service.rejectMaintenance(id, token, failure(409, "binding_mismatch"))
	}
	if renewed != token {
		if err := service.secrets.ReplaceIfExpected(ctx, secretReplacement{Reference: reference, Expected: token, Replacement: renewed}); err != nil {
			service.credentialFailure(record.TokenRef, err)
			return service.failedMaintenance(id, token, err)
		}
	}
	if renewed != token || recovered {
		lease.set(credentialState{state: maintenanceHostPending, tokenHash: tokenFingerprint(renewed)})
		if err := service.syncCredentialHost(callbackID, latest); err != nil {
			return maintenanceResult{ID: id, State: maintenanceHostPending, Error: safeCredentialCode(err)}
		}
	}
	lease.set(credentialState{state: maintenanceReady})
	return maintenanceResult{ID: id, State: maintenanceReady}
}

func maintenanceStateResult(id string, state credentialState) maintenanceResult {
	result := maintenanceResult{ID: id, State: state.state, Error: state.errCode}
	if !state.nextDue.IsZero() {
		result.NextDueAt = state.nextDue.UTC().Format(time.RFC3339)
	}
	return result
}

func (service *service) rejectMaintenance(id string, token sessionToken, err error) maintenanceResult {
	binding := service.settings().MaintenanceSources[id]
	state := credentialState{state: maintenanceCooldown, tokenHash: tokenFingerprint(token), nextDue: service.now().Add(maintenanceAuthCooldown), errCode: safeCredentialCode(err)}
	service.leases.get(binding.TokenRef).set(state)
	result := maintenanceStateResult(id, state)
	result.State = maintenanceSourceRejected
	return result
}

func (service *service) failedMaintenance(id string, token sessionToken, err error) maintenanceResult {
	binding := service.settings().MaintenanceSources[id]
	state := service.leases.get(binding.TokenRef).snapshot()
	if state.state == maintenanceFenced || state.state == maintenanceOperator {
		return maintenanceStateResult(id, state)
	}
	var authentication *AuthenticationFailure
	if errors.As(err, &authentication) {
		return service.rejectMaintenance(id, token, err)
	}
	switch safeCredentialCode(err) {
	case "session_renewal_identity_mismatch", "credential_identity_invalid":
		return service.rejectMaintenance(id, token, err)
	}
	return maintenanceResult{ID: id, State: maintenanceCredentialError, Error: safeCredentialCode(err)}
}

func (service *service) syncCredentialHost(callbackID string, record storageRecord) error {
	latest, _, err := service.findRecord(callbackID, record.ID)
	if err != nil {
		return failure(503, "host_sync_pending")
	}
	if latest.TokenRef != record.TokenRef {
		return failure(409, "binding_mismatch")
	}
	latest.SessionRevision++
	auth, err := authFromRecord(latest)
	if err != nil {
		return err
	}
	var saved struct {
		Name string `json:"name"`
	}
	if err := service.callback("host.auth.save", callbackRequest{HostCallbackID: callbackID, Name: latest.ID, JSON: auth.StorageJSON}, &saved); err != nil {
		return failure(503, "host_sync_pending")
	}
	return nil
}
