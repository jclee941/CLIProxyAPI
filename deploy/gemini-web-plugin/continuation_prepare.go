package main

import (
	"crypto/rand"
	"encoding/hex"
)

const continuationRetention = 16

func (service *service) prepareContinuation(execution continuationExecution) (interface{}, error) {
	if execution.local.State != localReady || execution.local.ContinuationActive != "" {
		return nil, failure(409, "session_busy")
	}
	parent := ""
	if execution.control.Token != "" {
		if execution.turn.State != "complete" {
			return nil, failure(409, "continuation_not_complete")
		}
		if execution.turn.NextToken != "" && !execution.request.freshContinuation {
			next, found := execution.turns[continuationKey(execution.turn.NextToken)]
			if !found {
				return nil, failure(410, "continuation_expired")
			}
			if next.Model != execution.request.Model {
				return nil, failure(409, "continuation_model_mismatch")
			}
			return continuationResponse(next.Model, continuationView{Token: execution.turn.NextToken, State: next.State}, nil)
		}
		parent = execution.turn.Metadata
	}
	sequence := uint64(0)
	oldestKey := ""
	oldest := ^uint64(0)
	for key, turn := range execution.turns {
		if turn.Sequence > sequence {
			sequence = turn.Sequence
		}
		if key != execution.key && turn.Sequence < oldest {
			oldestKey, oldest = key, turn.Sequence
		}
	}
	if sequence == ^uint64(0) {
		return nil, failure(409, "continuation_sequence_exhausted")
	}
	// A bounded receipt history does not bound conversation length. Evicted
	// receipts fail closed; their prompts are never treated as new submissions.
	if len(execution.turns) >= continuationRetention {
		delete(execution.turns, oldestKey)
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return nil, failure(500, "continuation_token_failed")
	}
	token := hex.EncodeToString(bytes)
	execution.turns[continuationKey(token)] = continuationTurn{CallerScope: execution.request.Metadata.CallerScope, Model: execution.request.Model, State: "prepared", Parent: parent, Sequence: sequence + 1}
	if execution.control.Token != "" {
		execution.turn.NextToken = token
		execution.turns[execution.key] = execution.turn
	}
	if err := service.saveContinuations(execution.local, execution.turns); err != nil {
		return nil, err
	}
	return continuationResponse(execution.request.Model, continuationView{Token: token, State: "prepared"}, nil)
}
