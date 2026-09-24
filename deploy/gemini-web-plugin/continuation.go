package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
)

const continuationField = "geminiWebContinuation"

// A receipt identifies one submission, not an account or a caller-supplied
// upstream identifier. Receipts are indexed by hash inside the encrypted session.
type continuationControl struct {
	Action string `json:"action"`
	Token  string `json:"token,omitempty"`
}
type continuationTurn struct {
	CallerScope  string   `json:"caller_scope"`
	Model        string   `json:"model"`
	State        string   `json:"state"`
	Parent       string   `json:"parent,omitempty"`
	Metadata     string   `json:"metadata,omitempty"`
	Conversation string   `json:"conversation,omitempty"`
	Reply        string   `json:"reply,omitempty"`
	Candidate    string   `json:"candidate,omitempty"`
	Digest       [32]byte `json:"digest"`
	Sequence     uint64   `json:"sequence"`
	StartedAt    int64    `json:"started_at,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	NextToken    string   `json:"next_token,omitempty"`
	ResultStored bool     `json:"result_stored,omitempty"`
	Background   bool     `json:"background,omitempty"`
	Error        string   `json:"error,omitempty"`
	ErrorMessage string   `json:"error_message,omitempty"`
}
type continuationResult struct {
	Payload []byte
	Headers http.Header
}

type continuationView struct {
	Token        string `json:"token"`
	State        string `json:"state"`
	Error        string `json:"error,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// continuationRequestLimit bounds the submitted body. It used to be sized for a
// prompt, which was right until an attachment could travel inline beside one:
// the base64 of even a short video is many times that, so a text-sized bound
// rejected the turn before anything could look at it. The generation request
// this body becomes is bounded again at the same size by omniRequest.
const continuationRequestLimit = 5 * 1024 * 1024

func continuationRequest(raw []byte) (continuationControl, []byte, bool, error) {
	var body map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil {
		return continuationControl{}, nil, false, nil
	}
	encoded, exists := body[continuationField]
	if !exists {
		return continuationControl{}, nil, false, nil
	}
	var control continuationControl
	if len(raw) > continuationRequestLimit || strictJSON(encoded, &control) != nil {
		return control, nil, true, failure(400, "invalid_continuation_request")
	}
	switch control.Action {
	case "prepare":
	case "submit", "recover":
		if control.Token == "" {
			return control, nil, true, failure(400, "continuation_token_required")
		}
	default:
		return control, nil, true, failure(400, "invalid_continuation_action")
	}
	if control.Token != "" {
		decoded, err := hex.DecodeString(control.Token)
		if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != control.Token {
			return control, nil, true, failure(400, "invalid_continuation_token")
		}
	}
	delete(body, continuationField)
	if control.Action != "submit" {
		if encoded, exists := body["contents"]; exists {
			var contents []json.RawMessage
			if json.Unmarshal(encoded, &contents) != nil || len(contents) != 0 {
				return control, nil, true, failure(400, "continuation_control_only")
			}
			delete(body, "contents")
		}
		if encoded, exists := body["generationConfig"]; exists {
			var config map[string]json.RawMessage
			if json.Unmarshal(encoded, &config) != nil || len(config) != 0 {
				return control, nil, true, failure(400, "continuation_control_only")
			}
			delete(body, "generationConfig")
		}
		if len(body) != 0 {
			return control, nil, true, failure(400, "continuation_control_only")
		}
	}
	payload, err := json.Marshal(body)
	return control, payload, true, err
}

func continuationKey(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func continuationTurns(local localSession) (map[string]continuationTurn, error) {
	turns := make(map[string]continuationTurn)
	if local.Continuations != "" && (strictJSON([]byte(local.Continuations), &turns) != nil || turns == nil) {
		return nil, failure(503, "continuation_store_corrupt")
	}
	return turns, nil
}

func (service *service) saveContinuations(local localSession, turns map[string]continuationTurn) error {
	raw, err := json.Marshal(turns)
	if err != nil {
		return failure(500, "continuation_encoding_failed")
	}
	// Keep encrypted application records below their existing bounded reader.
	if len(raw) > 48*1024 {
		return failure(409, "continuation_storage_full")
	}
	previous, err := continuationTurns(local)
	if err != nil {
		return err
	}
	local.Continuations = string(raw)
	if err := service.localStore().write(local); err != nil {
		return err
	}
	for key := range previous {
		if _, retained := turns[key]; !retained {
			if err := service.localStore().removeInteractionResult(key); err != nil {
				return err
			}
		}
	}
	return nil
}

func continuationResponse(model string, view continuationView, body []byte) (interface{}, error) {
	response := map[string]json.RawMessage{}
	if len(body) > 0 && json.Unmarshal(body, &response) != nil {
		return nil, failure(502, "invalid_continuation_response")
	}
	encodedModel, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	encodedView, err := json.Marshal(view)
	if err != nil {
		return nil, err
	}
	response["modelVersion"], response[continuationField] = encodedModel, encodedView
	encodedID, err := json.Marshal(view.Token)
	if err != nil {
		return nil, err
	}
	response["responseId"] = encodedID
	payload, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	return continuationResult{payload, http.Header{
		"Content-Type":                    {"application/json"},
		"X-Gemini-Web-Continuation-State": {view.State},
		"X-Gemini-Web-Continuation-Error": {view.Error},
	}}, nil
}

// executeContinuation owns the exclusive account lease throughout persistence
// and upstream I/O. Recovery may enter an interrupted *submission*, never renewal.
func (service *service) executeContinuation(ctx context.Context, request executorRequest) (interface{}, error) {
	control, payload, _, err := continuationRequest(request.Payload)
	if err != nil {
		return nil, err
	}
	if !service.settings().NativeGeneration || !service.settings().NativeContinuation {
		return nil, failure(400, "native_continuation_disabled")
	}
	if request.Stream || request.SourceFormat != "gemini" {
		return nil, failure(400, "continuation_native_nonstreaming_only")
	}
	if request.AuthProvider != provider || request.Metadata.PinnedAuthID != "" && request.Metadata.PinnedAuthID != request.AuthID {
		return nil, failure(400, "auth_identity_mismatch")
	}
	record, err := service.parseStorage(request.StorageJSON, false)
	if err != nil {
		return nil, err
	}
	if record.ID != request.AuthID {
		return nil, failure(400, "auth_identity_mismatch")
	}
	if record.Disabled {
		return nil, failure(409, "account_disabled")
	}
	if !localReferencePattern.MatchString(record.TokenRef) {
		return nil, failure(409, "continuation_requires_local_session")
	}
	lease, err := service.accountLease(record)
	if err != nil {
		return nil, err
	}
	if !waitForSlot(ctx, lease) {
		return nil, failure(409, "session_busy")
	}
	defer lease.guard.Unlock()
	local, err := service.localRecord(record)
	if err != nil {
		return nil, err
	}
	turns, err := continuationTurns(local)
	if err != nil {
		return nil, err
	}
	key := continuationKey(control.Token)
	turn, found := turns[key]
	if control.Token != "" && (!found || turn.CallerScope != request.Metadata.CallerScope) {
		return nil, failure(400, "continuation_identity_mismatch")
	}
	if local.State != localReady && !(local.State == localSubmitting && local.ContinuationActive == key) {
		return nil, failure(409, "session_requires_reconciliation")
	}
	state := lease.snapshot()
	if state.state == maintenanceHostPending && request.HostCallbackID != "" {
		// A manager re-login parks every account here for a while. The omni executor
		// resolves that by pushing the credential to the host instead of refusing, so
		// continuation does the same; otherwise native runs fail at random depending
		// on when they land relative to a login.
		if err := service.syncCredentialHost(request.HostCallbackID, record); err != nil {
			return nil, err
		}
		lease.set(credentialState{state: maintenanceReady})
		state = lease.snapshot()
	}
	if state.state == maintenanceFenced || state.state == maintenanceHostPending || state.state == maintenanceOperator && local.ContinuationActive != key {
		return nil, failure(409, "session_requires_reconciliation")
	}
	if control.Action == "prepare" {
		return service.prepareContinuation(continuationExecution{request: request, control: control, local: local, turns: turns, key: key, turn: turn})
	}
	if turn.Model != request.Model {
		return nil, failure(400, "continuation_model_mismatch")
	}
	return service.runContinuation(ctx, continuationExecution{request: request, control: control, payload: payload, local: local, turns: turns, key: key, turn: turn, lease: lease})
}
