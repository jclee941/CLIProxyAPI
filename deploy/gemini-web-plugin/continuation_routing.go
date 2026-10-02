package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
)

const continuationHeader = "X-Gemini-Web-Continuation"

func (service *service) interceptContinuation(raw []byte) requestInterceptResponse {
	var request struct {
		SourceFormat, Model, RequestedModel string
		Stream                              bool
		Headers                             http.Header
		Body                                []byte
		Metadata                            struct {
			CallerScope string `json:"caller_scope"`
		}
	}
	if json.Unmarshal(raw, &request) != nil {
		return interceptRequest(raw)
	}
	_, _, custom, _ := continuationRequest(request.Body)
	if custom {
		return interactionRejection(failure(400, "use_official_interactions_surface"))
	}
	if request.Model != interactionOmniModel && request.RequestedModel != interactionOmniModel {
		response := interceptRequest(raw)
		if request.Headers.Get(continuationHeader) != "" {
			response.ClearHeaders = []string{continuationHeader}
		}
		return response
	}
	if !service.settings().NativeContinuation || !service.settings().NativeGeneration {
		return interactionRejection(failure(400, "native_continuation_disabled"))
	}
	if request.SourceFormat != "interactions" {
		return interactionRejection(failure(400, "official_interactions_only"))
	}
	if !accountDigestPattern.MatchString(request.Metadata.CallerScope) {
		return interactionRejection(failure(400, "interaction_requires_authenticated_caller_scope"))
	}
	var retrieval struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(request.Body, &retrieval) == nil && retrieval.ID != "" {
		body, err := parseInteractionRetrieval(request.Body)
		if err != nil {
			return interactionRejection(err)
		}
		return requestInterceptResponse{ClearHeaders: []string{continuationHeader, interactionRetrieveHeader}, Headers: http.Header{continuationHeader: {body.ID}, interactionRetrieveHeader: {"true"}}}
	}
	body, _, err := parseInteraction(request.Body)
	if err != nil {
		return interactionRejection(err)
	}
	response := requestInterceptResponse{ClearHeaders: []string{continuationHeader, interactionRetrieveHeader}}
	// Continuations prefer the account that owns the conversation.
	if body.Previous != "" {
		response.Headers = http.Header{continuationHeader: {body.Previous}}
	}
	return response
}

func interactionRejection(err error) requestInterceptResponse {
	status := 400
	var public *publicError
	if errors.As(err, &public) {
		status = public.HTTPStatus
	}
	body, marshalErr := interactionErrorBody(err)
	if marshalErr != nil {
		return omniRejection(marshalErr)
	}
	return requestInterceptResponse{Terminate: true, StatusCode: status, ResponseHeaders: http.Header{"Content-Type": {"application/json"}}, ResponseBody: body}
}

// interactionErrorBody is the error object every Interactions failure answers
// with: its identifier as code, and its message as it stands. The message keeps
// the gemini_web_omni: prefix a failure raised during a turn carries, since the
// host's stop rules look for it there; the code is the bare identifier.
func interactionErrorBody(err error) ([]byte, error) {
	return interactionAccountErrorBody(accountDisplay{}, err)
}

// interactionAccountErrorBody is that object with the name and usage of the
// account that handled the turn, when one had been chosen. A refusal raised
// before any account is picked carries none.
func interactionAccountErrorBody(account accountDisplay, err error) ([]byte, error) {
	detail := map[string]any{
		"code":    strings.TrimPrefix(safeCredentialCode(err), "gemini_web_omni:"),
		"message": safeCredentialMessage(err),
	}
	account.put(detail)
	return json.Marshal(map[string]any{"error": detail})
}

// interactionFailure hands that object to the host as the failure's message.
// The host answers a message that is already JSON as it stands, where it would
// otherwise file the failure under a generic code for its status - a missing
// interaction read as model_not_found, a refused turn had no code at all. The
// code and status the host acts on do not change.
func interactionFailure(err error) error {
	return interactionAccountFailure(accountDisplay{}, err)
}

// interactionAccountFailure is interactionFailure for a turn an account was
// already chosen for, so the caller's error object names that account.
func interactionAccountFailure(account accountDisplay, err error) error {
	var public *publicError
	if !errors.As(err, &public) {
		return err
	}
	body, marshalErr := interactionAccountErrorBody(account, public)
	if marshalErr != nil {
		return err
	}
	return &publicError{Code: public.Code, Message: string(body), HTTPStatus: public.HTTPStatus}
}

type continuationPick struct {
	AuthID  string `json:"AuthID,omitempty"`
	Handled bool   `json:"Handled"`
}

// Schema 6 schedulers receive headers, not the body. The interceptor derives
// this header from the native request; the executor independently checks binding.
// Enabling this capability requires selecting this plugin as the host scheduler.
func (service *service) pickContinuation(raw []byte) (picked continuationPick, err error) {
	var request struct {
		Provider, Model string
		Providers       []string
		Options         struct {
			Headers  http.Header
			Metadata struct {
				CallerScope string `json:"caller_scope"`
			}
		}
		Candidates []struct{ ID, Provider string }
	}
	if json.Unmarshal(raw, &request) != nil {
		return continuationPick{}, failure(400, "invalid_scheduler_request")
	}
	// A refusal from here on answers an Interactions caller, whose error object
	// names the failure in code.
	if request.Model == interactionOmniModel {
		defer func() { err = interactionFailure(err) }()
	}
	token := request.Options.Headers.Get(continuationHeader)
	if token == "" {
		candidates := slices.DeleteFunc(slices.Clone(request.Candidates), func(candidate struct{ ID, Provider string }) bool {
			return !service.belowFirstEntryCeilings(candidate.ID)
		})
		pick := service.pickServableAccount(request.Model, candidates)
		if !pick.Handled && (request.Model == interactionOmniModel || request.Model == omniModel) {
			return continuationPick{}, failure(409, "account_unavailable")
		}
		return pick, nil
	}
	if !service.settings().NativeContinuation {
		return continuationPick{}, failure(400, "native_continuation_disabled")
	}
	if !accountDigestPattern.MatchString(request.Options.Metadata.CallerScope) {
		return continuationPick{}, failure(400, "interaction_requires_authenticated_caller_scope")
	}
	if request.Provider != provider && !(request.Provider == "" && slices.Contains(request.Providers, provider)) {
		return continuationPick{}, failure(400, "continuation_provider_mismatch")
	}
	store := service.localStore()
	if store == nil {
		return continuationPick{}, failure(503, "continuation_requires_local_session")
	}
	records, err := store.records()
	if err != nil {
		return continuationPick{}, err
	}
	key := continuationKey(token)
	retrieval := request.Options.Headers.Get(interactionRetrieveHeader) == "true"
	for _, local := range records {
		turns, errTurns := continuationTurns(local)
		if errTurns != nil {
			return continuationPick{}, errTurns
		}
		turn, found := turns[key]
		if !found || turn.CallerScope != request.Options.Metadata.CallerScope {
			continue
		}
		owner := local.Target.ID
		for _, candidate := range request.Candidates {
			if candidate.ID == owner && candidate.Provider == provider && (retrieval || service.ownerServes(local)) {
				return continuationPick{AuthID: owner, Handled: true}, nil
			}
		}
		// A retrieval has to land on the owner, the only store holding its result.
		// A create moves to another account only with a finished video to carry.
		if retrieval || turn.State != "complete" || !turn.ResultStored {
			return continuationPick{}, failure(409, "continuation_account_unavailable")
		}
		others := slices.DeleteFunc(slices.Clone(request.Candidates), func(candidate struct{ ID, Provider string }) bool {
			return candidate.ID == owner
		})
		if pick := service.pickServableAccount(request.Model, others); pick.Handled {
			return pick, nil
		}
		return continuationPick{}, failure(409, "continuation_account_unavailable")
	}
	if retrieval {
		return continuationPick{}, failure(404, "interaction_not_found")
	}
	return continuationPick{}, failure(400, "continuation_identity_mismatch")
}
