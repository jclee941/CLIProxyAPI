package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
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
	body, marshalErr := json.Marshal(map[string]any{"error": map[string]any{"code": status, "message": safeCredentialCode(err)}})
	if marshalErr != nil {
		return omniRejection(marshalErr)
	}
	return requestInterceptResponse{Terminate: true, StatusCode: status, ResponseHeaders: http.Header{"Content-Type": {"application/json"}}, ResponseBody: body}
}

type continuationPick struct {
	AuthID  string `json:"AuthID,omitempty"`
	Handled bool   `json:"Handled"`
}

// Schema 6 schedulers receive headers, not the body. The interceptor derives
// this header from the native request; the executor independently checks binding.
// Enabling this capability requires selecting this plugin as the host scheduler.
func (service *service) pickContinuation(raw []byte) (continuationPick, error) {
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
	token := request.Options.Headers.Get(continuationHeader)
	if token == "" {
		return service.pickServableAccount(request.Model, request.Candidates), nil
	}
	if !service.settings().NativeContinuation {
		return continuationPick{}, nil
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
	for _, local := range records {
		turns, err := continuationTurns(local)
		if err != nil {
			return continuationPick{}, err
		}
		if turn, found := turns[key]; !found || turn.CallerScope != request.Options.Metadata.CallerScope {
			continue
		}
		for _, candidate := range request.Candidates {
			if candidate.ID == local.Target.ID && candidate.Provider == provider {
				return continuationPick{AuthID: candidate.ID, Handled: true}, nil
			}
		}
		// Never return an invalid pick: the host treats it as a request to fall
		// back to its built-in scheduler. An explicit error fails closed instead.
		return continuationPick{}, failure(409, "continuation_account_unavailable")
	}
	if request.Options.Headers.Get(interactionRetrieveHeader) == "true" {
		return continuationPick{}, failure(404, "interaction_not_found")
	}
	return continuationPick{}, failure(400, "continuation_identity_mismatch")
}
