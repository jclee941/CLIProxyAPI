package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
)

const interactionRetrieveAlt = "interaction.get"
const interactionRetrieveHeader = "X-Gemini-Web-Interaction-Retrieve"

type interactionRetrieval struct {
	Model       string `json:"model"`
	ID          string `json:"id"`
	Stream      bool   `json:"stream,omitempty"`
	LastEventID string `json:"last_event_id,omitempty"`
}

func parseInteractionRetrieval(raw []byte) (interactionRetrieval, error) {
	var body interactionRetrieval
	if strictJSON(raw, &body) != nil || body.Model != interactionOmniModel {
		return body, failure(400, "invalid_interaction_retrieval")
	}
	token, err := hex.DecodeString(body.ID)
	if err != nil || len(token) != 32 || hex.EncodeToString(token) != body.ID {
		return body, failure(404, "interaction_not_found")
	}
	if _, err := interactionCursor(body.ID, body.LastEventID); err != nil {
		return body, err
	}
	return body, nil
}

func (service *service) retrieveInteraction(ctx context.Context, request executorRequest) (interface{}, error) {
	body, err := parseInteractionRetrieval(request.Payload)
	if err != nil {
		return nil, err
	}
	record, err := service.parseStorage(request.StorageJSON, false)
	if err != nil {
		return nil, err
	}
	if request.AuthProvider != provider || request.AuthID != record.ID || request.Metadata.PinnedAuthID != "" && request.Metadata.PinnedAuthID != record.ID {
		return nil, failure(400, "auth_identity_mismatch")
	}
	if record.Disabled {
		return nil, failure(409, "account_disabled")
	}
	local, err := service.localStore().read(record.TokenRef)
	if err != nil {
		return nil, err
	}
	if local.Target.ID != record.ID {
		return nil, failure(404, "interaction_not_found")
	}
	if local.Target.Disabled {
		return nil, failure(409, "account_disabled")
	}
	if err := service.checkLocalBinding(local.Target, local); err != nil {
		return nil, err
	}
	turns, err := continuationTurns(local)
	if err != nil {
		return nil, err
	}
	turn, found := turns[continuationKey(body.ID)]
	if !found || turn.CallerScope != request.Metadata.CallerScope || turn.Model != omniModel {
		return nil, failure(404, "interaction_not_found")
	}
	native := request
	native.Model, native.Format, native.SourceFormat, native.Alt, native.Stream = omniModel, "gemini", "gemini", "", false
	native.StorageJSON = []byte(local.Projection)
	native.Payload, err = json.Marshal(map[string]any{continuationField: continuationControl{Action: "recover", Token: body.ID}})
	if err != nil {
		return nil, err
	}
	service.interactionsMu.Lock()
	operation := service.interactions[body.ID]
	service.interactionsMu.Unlock()
	failed := turn.State == "failed" || turn.Background && turn.State == "no_video"
	if failed || turn.State == "no_operation" || turn.State == "complete" && turn.ResultStored {
		// An ended receipt is authoritative even if its old chat handles remain.
		// Like a stored completion, it must never enter upstream recovery again.
		view := continuationView{Token: body.ID, State: "outcome_unknown", Error: "missing_upstream_operation"}
		if turn.State == "no_video" {
			view.Error = "no_video_generated"
		}
		if turn.State == "failed" {
			view.Error, view.ErrorMessage = turn.Error, turn.ErrorMessage
		}
		var payload []byte
		if turn.State == "complete" {
			view.State, view.Error = "complete", ""
			payload, err = service.localStore().readInteractionResult(local, continuationKey(body.ID), turn.CallerScope)
			if err != nil {
				return nil, err
			}
		}
		result, err := renderInteraction(record.ID, continuationResult{Payload: payload}, view)
		if err != nil {
			return nil, err
		}
		if !request.Stream {
			return result, nil
		}
		operation = &interactionOperation{done: make(chan struct{}), result: result.(continuationResult)}
		close(operation.done)
	}
	if request.Stream {
		if request.StreamID == "" {
			return nil, failure(400, "interaction_stream_bridge_required")
		}
		cursor, err := interactionCursor(body.ID, body.LastEventID)
		if err != nil {
			return nil, err
		}
		if cursor > 1 && !turn.ResultStored && !((turn.State == "no_operation" || failed) && cursor == 2) {
			return nil, failure(400, "invalid_interaction_event_id")
		}
		if operation == nil {
			operation, err = service.ownInteraction(ctx, native, body.ID, nil, turn.Background)
			if err != nil {
				return nil, err
			}
		}
		return service.subscribeInteraction(request.StreamID, body.ID, cursor, operation)
	}
	if operation == nil && turn.Background {
		operation, err = service.ownInteraction(ctx, native, body.ID, nil, true)
		if err != nil {
			return nil, err
		}
	}
	if operation != nil {
		return renderInteraction(record.ID, continuationResult{}, continuationView{Token: body.ID, State: "pending"})
	}
	result, err := service.executeContinuation(ctx, native)
	if err != nil {
		return nil, err
	}
	response := result.(continuationResult)
	var receipt struct {
		View continuationView `json:"geminiWebContinuation"`
	}
	if err := json.Unmarshal(response.Payload, &receipt); err != nil {
		return nil, err
	}
	return renderInteraction(record.ID, response, receipt.View)
}
