package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
)

func (service *service) renewForVideo(ctx context.Context, record storageRecord, token sessionToken) (sessionToken, error) {
	reference, err := parseReference(record.TokenRef, service.settings().Vault)
	if err != nil {
		return sessionToken{}, err
	}
	if binding, bound := service.settings().MaintenanceSources[record.ID]; bound {
		authUser, err := tokenAuthUser(token)
		if err != nil {
			return sessionToken{}, err
		}
		if binding.TokenRef != record.TokenRef || binding.AuthUser != nil && *binding.AuthUser != authUser {
			return sessionToken{}, failure(409, "binding_mismatch")
		}
		identity, err := service.inspectCredential(ctx, record.TokenRef, token)
		if err != nil {
			return sessionToken{}, err
		}
		if identity.AccountSHA256 != binding.ExpectedGaiaSHA256 || identity.AuthUser != authUser {
			return sessionToken{}, failure(409, "credential_identity_mismatch")
		}
	}
	renewed, err := service.renewSession(ctx, record, token)
	if err != nil {
		return sessionToken{}, err
	}
	if renewed.value != token.value {
		err := service.replaceCredential(ctx, secretReplacement{Reference: reference, Expected: token, Replacement: renewed})
		if err != nil {
			service.credentialFailure(record.TokenRef, err)
			return sessionToken{}, failure(503, "session_renewal_persistence_failed")
		}
		service.leases.get(record.TokenRef).set(credentialState{state: maintenanceHostPending, tokenHash: tokenFingerprint(renewed)})
	}
	return renewed, nil
}

func (service *service) renewSession(ctx context.Context, record storageRecord, token sessionToken) (sessionToken, error) {
	authUser, err := tokenAuthUser(token)
	if err != nil {
		return sessionToken{}, err
	}
	binding, bound := service.settings().MaintenanceSources[record.ID]
	if bound && (binding.TokenRef != record.TokenRef || binding.AuthUser != nil && *binding.AuthUser != authUser) {
		return sessionToken{}, failure(409, "binding_mismatch")
	}
	response, err := service.credentialHTTP(ctx, record.TokenRef, sidecarRequest{Method: "POST", Path: "/v1/session/renew", Token: token, Body: []byte("{}")})
	if err != nil {
		return sessionToken{}, err
	}
	var body struct {
		Token         string  `json:"token"`
		AccountSHA256 string  `json:"account_sha256"`
		AuthUser      *uint64 `json:"auth_user"`
	}
	if response.StatusCode != 200 || strictJSON(response.Body, &body) != nil {
		return sessionToken{}, failure(502, "session_renewal_invalid")
	}
	renewed, err := parseToken(body.Token)
	if err != nil {
		return sessionToken{}, failure(502, "session_renewal_invalid")
	}
	renewedUser, err := tokenAuthUser(renewed)
	if err != nil || renewedUser != authUser {
		return sessionToken{}, failure(502, "session_renewal_identity_mismatch")
	}
	if body.AuthUser != nil && *body.AuthUser != authUser || body.AccountSHA256 != "" && !accountDigestPattern.MatchString(body.AccountSHA256) || bound && (body.AuthUser == nil || body.AccountSHA256 != binding.ExpectedGaiaSHA256) {
		return sessionToken{}, failure(502, "session_renewal_identity_mismatch")
	}
	return renewed, nil
}

type AuthenticationFailure struct{}

func (*AuthenticationFailure) Error() string { return "auth_error" }
func (*AuthenticationFailure) Unwrap() error { return failure(401, "auth_error") }

func (service *service) credentialHTTP(ctx context.Context, reference string, request sidecarRequest) (httpResponse, error) {
	credentialContext, cancel := context.WithTimeout(ctx, credentialFenceDuration)
	defer cancel()
	tagged := request
	tagged.Reference = reference
	response, err := service.sidecar(credentialContext, tagged)
	if err != nil {
		service.credentialFailure(reference, err)
	}
	return response, err
}

func (service *service) inspectCredential(ctx context.Context, reference string, token sessionToken) (credentialInspection, error) {
	response, err := service.credentialHTTP(ctx, reference, sidecarRequest{Method: "POST", Path: "/v1/session/inspect", Token: token, Body: []byte("{}")})
	if err != nil {
		return credentialInspection{}, err
	}
	var wire struct {
		AccountSHA256 string  `json:"account_sha256"`
		AuthUser      *uint64 `json:"auth_user"`
	}
	if response.StatusCode != 200 || strictJSON(response.Body, &wire) != nil || wire.AuthUser == nil || !accountDigestPattern.MatchString(wire.AccountSHA256) {
		return credentialInspection{}, failure(502, "credential_identity_invalid")
	}
	return credentialInspection{AccountSHA256: wire.AccountSHA256, AuthUser: *wire.AuthUser}, nil
}

func tokenAuthUser(token sessionToken) (uint64, error) {
	checked, err := parseToken(token.value)
	if err != nil {
		return 0, err
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(checked.value, "gemini-web:v1:"))
	if err != nil {
		return 0, failure(400, "invalid_session_token")
	}
	var session struct {
		AuthUser uint64 `json:"auth_user"`
	}
	if json.Unmarshal(payload, &session) != nil {
		return 0, failure(400, "invalid_session_token")
	}
	return session.AuthUser, nil
}
