package main

import (
	"context"
)

type detachResult struct {
	ID       string `json:"id"`
	Detached bool   `json:"detached"`
}

// The detached marker keeps the account usable both before and after the operator
// deletes the maintenance_sources entry; checkLocalBinding rejects both otherwise.
func (service *service) detachLegacyBinding(ctx context.Context, request managementRequest) (interface{}, error) {
	var body struct {
		ID      string `json:"id"`
		Consent bool   `json:"consent"`
	}
	if len(request.Body) > 40000 || strictJSON(request.Body, &body) != nil || !body.Consent {
		return nil, failure(400, "invalid_detach_request")
	}
	record, _, err := service.findRecord(request.HostCallbackID, body.ID)
	if err != nil {
		return nil, err
	}
	if !localReferencePattern.MatchString(record.TokenRef) {
		return nil, failure(409, "local_session_required")
	}
	store := service.localStore()
	if store == nil {
		return nil, failure(503, loginConfigurationError)
	}
	lease, err := service.accountLease(record)
	if err != nil {
		return nil, err
	}
	if !lease.guard.TryLock() {
		return nil, failure(409, "session_busy")
	}
	defer lease.guard.Unlock()
	local, err := store.read(record.TokenRef)
	if err != nil {
		return nil, err
	}
	if local.State != localReady {
		return nil, failure(409, "detach_requires_ready_session")
	}
	if err := service.checkLocalBinding(local.Target, local); err != nil {
		return nil, err
	}
	if !sameHostProjection(record, local.Target) {
		return nil, failure(409, "credential_changed")
	}
	if local.LegacyRef == "" {
		return detachResult{record.ID, true}, nil
	}
	local.LegacyRef, local.LegacyGUID, local.LegacyUserBound, local.LegacyDetached = "", "", false, true
	if err := store.write(local); err != nil {
		return nil, err
	}
	return detachResult{record.ID, true}, nil
}
