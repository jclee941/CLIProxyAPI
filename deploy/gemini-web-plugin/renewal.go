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
	authUser, err := tokenAuthUser(token)
	if err != nil {
		return sessionToken{}, err
	}
	response, err := service.sidecar(ctx, sidecarRequest{Method: "POST", Path: "/v1/session/renew", Token: token})
	if err != nil {
		return sessionToken{}, err
	}
	var body struct {
		Token string `json:"token"`
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
	if renewed.value != token.value {
		stored, err := service.secrets.Put(ctx, secretWrite{Label: record.Label, Token: renewed, Existing: reference})
		if err != nil {
			return sessionToken{}, failure(503, "session_renewal_persistence_failed")
		}
		if stored != reference {
			return sessionToken{}, failure(502, "session_renewal_reference_mismatch")
		}
	}
	return renewed, nil
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
