package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
)

// Credential upkeep talks to Google per account rather than through the bridge,
// so a failure is attributed to the credential it belongs to.
func (service *service) renewCredential(ctx context.Context, reference string, token sessionToken) (sessionToken, credentialInspection, error) {
	credentialContext, cancel := context.WithTimeout(ctx, credentialFenceDuration)
	defer cancel()
	renewed, identity, err := service.nativeRenew(credentialContext, reference, token)
	if err != nil && reference != "" {
		service.credentialFailure(reference, err)
	}
	return renewed, identity, err
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
	if err != nil && reference != "" {
		service.credentialFailure(reference, err)
	}
	return response, err
}

func (service *service) inspectCredential(ctx context.Context, reference string, token sessionToken) (credentialInspection, error) {
	credentialContext, cancel := context.WithTimeout(ctx, credentialFenceDuration)
	defer cancel()
	identity, err := service.nativeInspect(credentialContext, reference, token)
	if err != nil {
		if reference != "" {
			service.credentialFailure(reference, err)
		}
		return credentialInspection{}, err
	}
	if !accountDigestPattern.MatchString(identity.AccountSHA256) {
		return credentialInspection{}, failure(502, "credential_identity_invalid")
	}
	return identity, nil
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
