package main

import (
	"context"
)

const recoverPath = "/v0/management/plugins/gemini-web/recover"

type recoverView struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Released bool   `json:"released"`
	Error    string `json:"error,omitempty"`
}

// recoverIntent runs continuation recovery for the turn an account is pinned on.
// The caller's receipt only names that turn and authorises the caller; the
// session already records which turn it is, so an operator can finish a
// generation whose receipt was lost instead of leaving the account stranded.
func (service *service) recoverIntent(ctx context.Context, request managementRequest) (interface{}, error) {
	var body struct {
		ID      string `json:"id"`
		Consent bool   `json:"consent"`
	}
	if len(request.Body) > 40000 || strictJSON(request.Body, &body) != nil || !body.Consent {
		return nil, failure(400, "invalid_recover_request")
	}
	record, enabled, err := service.findRecord(request.HostCallbackID, body.ID)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, failure(409, "account_disabled")
	}
	if !localReferencePattern.MatchString(record.TokenRef) {
		return nil, failure(409, "local_session_required")
	}
	lease, err := service.accountLease(record)
	if err != nil {
		return nil, err
	}
	if !lease.guard.TryLock() {
		return nil, failure(409, "session_busy")
	}
	defer lease.guard.Unlock()
	local, err := service.localRecord(record)
	if err != nil {
		return nil, err
	}
	key := local.ContinuationActive
	if key == "" {
		return nil, failure(409, "resolve_requires_interrupted_operation")
	}
	turns, err := continuationTurns(local)
	if err != nil {
		return nil, err
	}
	turn, found := turns[key]
	if !found {
		return nil, failure(409, "continuation_recovery_required")
	}
	auth, err := authFromRecord(record)
	if err != nil {
		return nil, err
	}
	execution := executorRequest{AuthID: record.ID, AuthProvider: provider, Model: turn.Model, Format: "gemini", SourceFormat: "gemini", StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, HostCallbackID: request.HostCallbackID}
	view := recoverView{ID: record.ID}
	if _, err := service.runContinuation(ctx, continuationExecution{request: execution, control: continuationControl{Action: "recover"}, local: local, turns: turns, key: key, turn: turn, lease: lease}); err != nil {
		view.Error = safeCredentialCode(err)
	}
	settled, err := service.localStore().read(record.TokenRef)
	if err != nil {
		return nil, err
	}
	view.State, view.Released = string(settled.State), settled.ContinuationActive == ""
	return view, nil
}
