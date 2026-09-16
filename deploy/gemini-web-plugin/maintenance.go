package main

import (
	"context"
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

type maintenanceTarget struct {
	callbackID string
	id         string
	explicit   bool
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
	localIDs := make(map[string]bool)
	if store := service.localStore(); store != nil {
		records, err := store.records()
		if err != nil {
			return maintenanceResponse{}, err
		}
		for _, record := range records {
			localIDs[record.Target.ID] = true
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
	ids := make([]string, 0, len(localIDs))
	for id := range localIDs {
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
		target := maintenanceTarget{callbackID: request.HostCallbackID, id: id, explicit: selectedID != ""}
		result.Results = append(result.Results, service.maintainLocalAccount(ctx, target))
	}
	return result, nil
}

func maintenanceStateResult(id string, state credentialState) maintenanceResult {
	result := maintenanceResult{ID: id, State: state.state, Error: state.errCode}
	if !state.nextDue.IsZero() {
		result.NextDueAt = state.nextDue.UTC().Format(time.RFC3339)
	}
	return result
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
