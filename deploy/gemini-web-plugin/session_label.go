package main

import (
	"context"
	"strings"
)

type labelResult struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	State string `json:"state"`
}

// relabelAccount never reads, renews, or replaces the credential: only the host
// projection advances. A record that is not ready is refused so that renaming
// can never mask an interrupted operation.
func (service *service) relabelAccount(ctx context.Context, request managementRequest) (interface{}, error) {
	var body struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	if len(request.Body) > 40000 || strictJSON(request.Body, &body) != nil || strings.TrimSpace(body.Label) == "" || len(body.Label) > 200 || strings.ContainsAny(body.Label, "\r\n\x00") {
		return nil, failure(400, "invalid_label_request")
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
	if err := service.checkLocalBinding(local.Target, local); err != nil {
		return nil, err
	}
	if local.State != localReady {
		return nil, failure(409, "label_requires_ready_session")
	}
	if !sameHostProjection(record, local.Target) {
		return nil, failure(409, "credential_changed")
	}
	if local.Target.Label == body.Label {
		return labelResult{local.Target.ID, local.Target.Label, string(loginSaved)}, nil
	}
	if local.Target.SessionRevision == ^uint64(0) {
		return nil, failure(409, "session_revision_exhausted")
	}
	local.Previous = local.Target
	local.Target.Label = body.Label
	local.Target.SessionRevision++
	auth, err := authFromRecord(local.Target)
	if err != nil {
		return nil, err
	}
	local.Projection = string(auth.StorageJSON)
	local.State = localHostPending
	if err := store.write(local); err != nil {
		return nil, err
	}
	view, err := service.syncLocalHost(ctx, request.HostCallbackID, local)
	if err != nil {
		return nil, err
	}
	return labelResult{local.Target.ID, local.Target.Label, string(view.Status)}, nil
}
