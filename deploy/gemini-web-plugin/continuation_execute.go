package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
)

type continuationExecution struct {
	request executorRequest
	control continuationControl
	payload []byte
	local   localSession
	turns   map[string]continuationTurn
	key     string
	turn    continuationTurn
	lease   *credentialLease
}

func (service *service) runContinuation(ctx context.Context, execution continuationExecution) (interface{}, error) {
	turn := execution.turn
	view := continuationView{Token: execution.control.Token, State: turn.State}
	if execution.control.Action == "recover" && turn.State == "prepared" {
		return continuationResponse(turn.Model, view, nil)
	}
	if execution.control.Action == "recover" && turn.State == "complete" && turn.ResultStored {
		body, err := service.localStore().readInteractionResult(execution.local, execution.key, turn.CallerScope)
		if err != nil {
			return nil, err
		}
		return continuationResponse(turn.Model, view, body)
	}
	if !hasOmniStopRules(execution.request.AuthMetadata.RequestScopedErrors) {
		return nil, failure(400, "continuation_requires_host_request_stop_policy")
	}
	var prompt string
	var options omniOptions
	var media []webMedia
	var err error
	if execution.control.Action == "submit" {
		// Both modalities deliberately accept only one new user turn. History and
		// raw upstream IDs are never accepted from the caller.
		var body map[string]json.RawMessage
		if json.Unmarshal(execution.payload, &body) != nil {
			return nil, failure(400, "invalid_continuation_request")
		}
		delete(body, "model")
		normalized, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		prompt, options, err = omniRequest(normalized)
		if err != nil {
			return nil, err
		}
		// Reference media is validated with the prompt but uploaded later, once
		// the session that will carry the generation is the final one.
		if _, media, err = webContentsToPrompt(normalized); err != nil {
			return nil, err
		}
		digest := sha256.Sum256(normalized)
		if turn.State != "prepared" && turn.Digest != digest {
			return nil, failure(409, "continuation_submission_conflict")
		}
		turn.Digest = digest
	}
	credential, err := decodeWebCredential(sessionToken{execution.local.Token})
	if err != nil {
		return nil, err
	}
	identity, err := service.webIdentity(ctx, execution.local.Target.TokenRef, credential)
	if err != nil {
		return nil, err
	}
	if identity != execution.local.Identity {
		return nil, failure(409, "credential_identity_mismatch")
	}
	session := service.newSession(credential)
	service.trackJar(execution.local.Target.TokenRef, session)
	defer service.persistJar(execution.local.Target.TokenRef, session)
	if turn.State == "prepared" {
		account, err := session.webCapabilities(ctx)
		if err != nil {
			return nil, err
		}
		model, ok := webSelectCapability(account, turn.Model)
		if turn.Model == omniModel {
			model, ok = webVideoCapability(account)
		}
		if !ok {
			return nil, failure(404, "account_model_unavailable")
		}
		if turn.Model == omniModel {
			token, err := service.renewLocalSession(ctx, execution.request.HostCallbackID, execution.local.Target)
			if err != nil {
				return nil, err
			}
			execution.local, err = service.localStore().read(execution.local.Target.TokenRef)
			if err != nil {
				return nil, err
			}
			credential, err = decodeWebCredential(token)
			if err != nil {
				return nil, err
			}
			session = service.newSession(credential)
			service.trackJar(execution.local.Target.TokenRef, session)
		}
		if err := session.bootstrap(ctx); err != nil {
			return nil, err
		}
		nonce, err := webConversationID()
		if err != nil {
			return nil, err
		}
		sources, err := service.mediaSources(ctx, media)
		if err != nil {
			return nil, err
		}
		attachments, err := session.uploadSources(ctx, sources)
		if err != nil {
			return nil, err
		}
		fields := webGenerationFields(prompt, model.Mode, webThinkingDefault, nonce, attachments)
		if turn.Model == omniModel {
			fields = webVideoFields(options.applyPrompt(prompt), model.Mode, nonce, options.framing(), attachments)
		}
		if turn.Parent != "" {
			var metadata []any
			if json.Unmarshal([]byte(turn.Parent), &metadata) != nil {
				return nil, failure(503, "continuation_store_corrupt")
			}
			fields[2] = metadata
		}
		encoded, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		turn.State, turn.StartedAt = "submitting", service.now().Unix()
		turn.Summary = webTurnSummary(prompt, attachments)
		execution.local.State, execution.local.ContinuationActive = localSubmitting, execution.key
		execution.turns[execution.key] = turn
		if err := service.saveContinuations(execution.local, execution.turns); err != nil {
			return nil, err
		}
		session.generationFrame = func(raw []byte) error {
			updated, err := continuationFrame(turn, raw)
			if err != nil {
				return err
			}
			if updated == turn {
				return nil
			}
			turn = updated
			execution.turns[execution.key] = turn
			return service.saveContinuations(execution.local, execution.turns)
		}
		_, submitErr := session.postGeneration(ctx, string(encoded), account, model)
		session.generationFrame = nil
		if submitErr != nil {
			view.State, view.Error = "pending", safeCredentialCode(submitErr)
			if turn.Conversation == "" || turn.Reply == "" || turn.Candidate == "" {
				// The stream died before it named an operation, so there is
				// nothing for recovery to find and nothing to protect. Holding
				// the account until the generation budget expires only takes it
				// out of rotation for ten minutes on a turn already known to be
				// unobservable, which is how a healthy fleet runs out of accounts.
				view.State = "outcome_unknown"
				turn.State = "no_operation"
				execution.turns[execution.key] = turn
				if execution.local.ContinuationActive == execution.key {
					execution.local.State, execution.local.ContinuationActive = localReady, ""
				}
				if saveErr := service.saveContinuations(execution.local, execution.turns); saveErr != nil {
					return nil, saveErr
				}
				execution.lease.set(credentialState{state: maintenanceReady})
			}
			return continuationResponse(turn.Model, view, nil)
		}
	}
	if turn.Conversation == "" || turn.Reply == "" || turn.Candidate == "" {
		return continuationResponse(turn.Model, continuationView{Token: view.Token, State: "outcome_unknown", Error: "missing_upstream_operation"}, nil)
	}
	// One observation per request: recovery never calls StreamGenerate and never
	// polls or sleeps. The caller may issue another recover while still pending.
	turns, err := session.rpc(ctx, webVideoTurnsRPC, []any{turn.Conversation, 32, nil, 1, []any{1}, []any{4}, nil, 1})
	if err != nil {
		return continuationResponse(turn.Model, continuationView{Token: view.Token, State: "pending", Error: safeCredentialCode(err)}, nil)
	}
	candidate, err := continuationCandidate(turn, turns)
	if err != nil {
		return nil, err
	}
	var body []byte
	if turn.Model == omniModel {
		state, err := webParseVideoCandidate(candidate)
		if err != nil {
			// The reply exists but terminally carries no video, so this turn can
			// never complete. Releasing the durable intent here is what keeps the
			// account usable: the operator route defers to recovery, and recovery
			// would otherwise return this same answer forever on a pinned session.
			turn.State = "no_video"
			execution.turns[execution.key] = turn
			if execution.local.ContinuationActive == execution.key {
				execution.local.State, execution.local.ContinuationActive = localReady, ""
			}
			if saveErr := service.saveContinuations(execution.local, execution.turns); saveErr != nil {
				return nil, saveErr
			}
			// The credential answered definitively; only this turn failed, so the
			// lease must leave the operator state with the session it was pinned to.
			execution.lease.set(credentialState{state: maintenanceReady})
			return nil, err
		}
		if !state.Ready {
			return continuationResponse(turn.Model, continuationView{Token: view.Token, State: "pending"}, nil)
		}
		status, video, err := session.downloadVideo(ctx, state.URL)
		if err != nil {
			return continuationResponse(turn.Model, continuationView{Token: view.Token, State: "pending", Error: safeCredentialCode(err)}, nil)
		}
		if status == http.StatusPartialContent {
			return continuationResponse(turn.Model, continuationView{Token: view.Token, State: "pending"}, nil)
		}
		if status != http.StatusOK || len(video) < 8 || string(video[4:8]) != "ftyp" {
			return nil, failure(502, "invalid_video_download")
		}
		body, err = json.Marshal(map[string]any{"candidates": []any{map[string]any{"index": 0, "finishReason": "STOP", "content": map[string]any{"role": "model", "parts": []any{map[string]any{"inlineData": map[string]string{"mimeType": "video/mp4", "data": base64.StdEncoding.EncodeToString(video)}}}}}}})
		if err != nil {
			return nil, err
		}
	} else {
		if jsonField(candidate, 8, 0) != float64(2) {
			return continuationResponse(turn.Model, continuationView{Token: view.Token, State: "pending"}, nil)
		}
		text, ok := jsonField(candidate, 1, 0).(string)
		if !ok || text == "" {
			return nil, failure(502, "web_response_invalid")
		}
		body, err = webRenderResponse(turn.Model, text, []byte(`{}`))
		if err != nil {
			return nil, err
		}
	}
	if turn.Model == omniModel {
		if err := service.localStore().writeInteractionResult(execution.local, execution.key, turn.CallerScope, body); err != nil {
			return nil, err
		}
		turn.ResultStored = true
	}
	turn.State = "complete"
	execution.turns[execution.key] = turn
	// An old completed receipt can be re-read while a newer turn is idle; it
	// cannot clear a different operation's durable submission intent.
	if execution.local.ContinuationActive == execution.key {
		execution.local.State, execution.local.ContinuationActive = localReady, ""
	}
	if err := service.saveContinuations(execution.local, execution.turns); err != nil {
		return nil, err
	}
	execution.lease.set(credentialState{state: maintenanceReady})
	return continuationResponse(turn.Model, continuationView{Token: view.Token, State: "complete"}, body)
}
