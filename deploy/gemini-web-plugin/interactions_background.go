package main

import (
	"context"
	"encoding/json"
	"errors"
)

// The request scope is not a callback lease: native host.auth callbacks use the
// plugin-lifetime C bridge, unlike host HTTP/model streams. Renewal can therefore
// keep using HostCallbackID after the unary executor returns. Upstream traffic
// here uses the plugin's own client and ownInteraction's lifecycle context.
func (service *service) markBackgroundInteraction(ctx context.Context, request executorRequest, token string) error {
	record, err := service.parseStorage(request.StorageJSON, false)
	if err != nil {
		return err
	}
	lease, err := service.accountLease(record)
	if err != nil {
		return err
	}
	if !waitForSlot(ctx, lease) {
		return failure(409, "session_busy")
	}
	defer lease.guard.Unlock()
	local, err := service.localStore().read(record.TokenRef)
	if err != nil {
		return err
	}
	turns, err := continuationTurns(local)
	if err != nil {
		return err
	}
	key := continuationKey(token)
	turn, found := turns[key]
	if !found {
		return failure(404, "interaction_not_found")
	}
	turn.Background = true
	turns[key] = turn
	return service.saveContinuations(local, turns)
}

// Once POST has acknowledged the receipt, execution failures belong to that
// resource rather than a vanished request. Save them before closing done; GET
// and a new process must return the same failed object without upstream I/O.
func (service *service) persistBackgroundOutcome(request executorRequest, token string, result interface{}, executionErr error) (interface{}, error) {
	if errors.Is(executionErr, context.Canceled) {
		// Shutdown is not a terminal upstream result. A named submission remains
		// recoverable after restart, and recovery never resubmits a prepared one.
		return nil, executionErr
	}
	var outcome struct {
		Status string
		Error  struct{ Code, Message string }
	}
	if executionErr != nil {
		outcome.Status = "failed"
		outcome.Error.Code, outcome.Error.Message = safeCredentialCode(executionErr), safeCredentialMessage(executionErr)
	} else {
		if err := json.Unmarshal(result.(continuationResult).Payload, &outcome); err != nil {
			return nil, err
		}
		if outcome.Status == "completed" {
			return result, nil
		}
	}
	record, err := service.parseStorage(request.StorageJSON, false)
	if err != nil {
		return nil, errors.Join(err, executionErr)
	}
	lease, err := service.accountLease(record)
	if err != nil {
		return nil, errors.Join(err, executionErr)
	}
	lease.guard.Lock()
	defer lease.guard.Unlock()
	local, err := service.localStore().read(record.TokenRef)
	if err != nil {
		return nil, errors.Join(err, executionErr)
	}
	turns, err := continuationTurns(local)
	if err != nil {
		return nil, errors.Join(err, executionErr)
	}
	key := continuationKey(token)
	turn, found := turns[key]
	if !found {
		return nil, failure(404, "interaction_not_found")
	}
	if outcome.Status == "in_progress" {
		if turn.State != "prepared" {
			return result, nil
		}
		// The process ended between accepting and submitting. GET has no prompt
		// to resubmit, and must not leave an abandoned job in progress forever.
		outcome.Error.Code, outcome.Error.Message = "background_execution_interrupted", "background_execution_interrupted"
	}
	turn.State, turn.Error, turn.ErrorMessage = "failed", outcome.Error.Code, outcome.Error.Message
	turns[key] = turn
	if local.State == localSubmitting && local.ContinuationActive == key {
		local.State, local.ContinuationActive = localReady, ""
	}
	if err := service.saveContinuations(local, turns); err != nil {
		return nil, errors.Join(err, executionErr)
	}
	return renderInteraction(record.ID, continuationResult{}, continuationView{
		Token: token, State: "outcome_unknown", Error: turn.Error, ErrorMessage: turn.ErrorMessage,
	})
}
