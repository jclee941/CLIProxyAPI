package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// Credential upkeep speaks to Google directly rather than through the bridge.
// Renewal is one call to the fixed rotation origin followed by a fresh bootstrap
// that proves the rotated jar still reaches the same account.

const webRotateOrigin = "https://accounts.google.com"

// webRotatePayload is the body the browser sends; both numbers are placeholders
// the endpoint ignores, and it is written out verbatim because the leading zeros
// are not valid JSON to re-encode.
var webRotatePayload = []byte(`[000,"-0000000000000000000"]`)

// The account is identified by three page globals that must agree on one GAIA
// id; its digest is what a credential stays bound to across a rotation.
var (
	webWizAssignment = regexp.MustCompile(`\bWIZ_global_data\s*=\s*`)
	webGaiaPattern   = regexp.MustCompile(`^[0-9]{21}$`)
)

// webRotatableDomains are the only cookie domains a rotation may write, so a
// response cannot widen the jar to an unrelated origin.
var webRotatableDomains = map[string]bool{"google.com": true, "gemini.google.com": true}

func webAccountDigest(page string) string {
	assignments := webWizAssignment.FindAllStringIndex(page, -1)
	if len(assignments) != 1 {
		return ""
	}
	var globals struct {
		First  string `json:"S06Grb"`
		Second string `json:"W3Yyqf"`
		Third  string `json:"qDCSke"`
	}
	if json.NewDecoder(strings.NewReader(page[assignments[0][1]:])).Decode(&globals) != nil {
		return ""
	}
	if globals.First != globals.Second || globals.First != globals.Third || !webGaiaPattern.MatchString(globals.First) {
		return ""
	}
	digest := sha256.Sum256([]byte(globals.First))
	return hex.EncodeToString(digest[:])
}

func encodeWebCredential(cookie string, authUser int) sessionToken {
	payload, err := json.Marshal(webCredential{Cookie: cookie, AuthUser: authUser})
	if err != nil {
		return sessionToken{}
	}
	return sessionToken{"gemini-web:v1:" + base64.RawURLEncoding.EncodeToString(payload)}
}

// webMergeCookies replaces the rotated values in place and appends the ones the
// jar did not have, so cookie order and everything the rotation left alone are
// preserved.
func webMergeCookies(cookie string, updates map[string]string) string {
	if len(updates) == 0 {
		return cookie
	}
	present := map[string]bool{}
	pairs := make([]string, 0, len(updates))
	for _, pair := range strings.Split(cookie, ";") {
		trimmed := strings.TrimSpace(pair)
		name, _, _ := strings.Cut(trimmed, "=")
		present[name] = true
		if value, rotated := updates[name]; rotated {
			pairs = append(pairs, name+"="+value)
			continue
		}
		pairs = append(pairs, trimmed)
	}
	for name, value := range updates {
		if !present[name] {
			pairs = append(pairs, name+"="+value)
		}
	}
	return strings.Join(pairs, "; ")
}

func (service *service) rotateCookies(ctx context.Context, credential webCredential) (string, error) {
	origin := webRotateOrigin
	if service.webRotateOverride != "" {
		origin = service.webRotateOverride
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/RotateCookies", strings.NewReader(string(webRotatePayload)))
	if err != nil {
		return "", failure(400, "web_request_invalid")
	}
	request.Header.Set("Cookie", credential.Cookie)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	request.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	response, err := service.client.Do(request)
	if err != nil {
		return "", failure(502, "session_rotation_transport_failed")
	}
	defer func() {
		if _, copyErr := io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20)); copyErr != nil {
			_ = copyErr
		}
		if closeErr := response.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()
	if response.StatusCode == http.StatusUnauthorized {
		return "", failure(401, "session_rotation_unauthenticated")
	}
	if response.StatusCode != http.StatusOK {
		return "", failure(502, "session_rotation_rejected")
	}
	updates := map[string]string{}
	for _, cookie := range response.Cookies() {
		domain := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")
		if cookie.Path != "/" || !webRotatableDomains[domain] {
			continue
		}
		updates[cookie.Name] = cookie.Value
	}
	return webMergeCookies(credential.Cookie, updates), nil
}

func (service *service) webIdentity(ctx context.Context, reference string, credential webCredential) (credentialInspection, error) {
	session := service.newSession(credential)
	service.trackJar(reference, session)
	defer service.persistJar(reference, session)
	page, err := session.do(ctx, session.prefix+"/app", nil, nil)
	if err != nil {
		return credentialInspection{}, err
	}
	digest := webAccountDigest(string(page))
	if !accountDigestPattern.MatchString(digest) {
		return credentialInspection{}, failure(502, "credential_identity_invalid")
	}
	return credentialInspection{AccountSHA256: digest, AuthUser: uint64(credential.AuthUser)}, nil
}

func (service *service) nativeInspect(ctx context.Context, reference string, token sessionToken) (credentialInspection, error) {
	credential, err := decodeWebCredential(token)
	if err != nil {
		return credentialInspection{}, err
	}
	return service.webIdentity(ctx, reference, credential)
}

// nativeRenew rotates the jar and then proves the rotated credential still
// reaches the same account, so a rotation that silently moved accounts is
// refused rather than persisted.
func (service *service) nativeRenew(ctx context.Context, reference string, token sessionToken) (sessionToken, credentialInspection, error) {
	credential, err := decodeWebCredential(token)
	if err != nil {
		return sessionToken{}, credentialInspection{}, err
	}
	before, err := service.webIdentity(ctx, reference, credential)
	if err != nil {
		return sessionToken{}, credentialInspection{}, err
	}
	cookie, err := service.rotateCookies(ctx, credential)
	if err != nil {
		return sessionToken{}, credentialInspection{}, err
	}
	rotated := webCredential{Cookie: cookie, AuthUser: credential.AuthUser}
	after, err := service.webIdentity(ctx, reference, rotated)
	if err != nil {
		return sessionToken{}, credentialInspection{}, err
	}
	if after != before {
		return sessionToken{}, credentialInspection{}, failure(502, "session_renewal_identity_mismatch")
	}
	renewed := encodeWebCredential(cookie, credential.AuthUser)
	if renewed.value == "" {
		return sessionToken{}, credentialInspection{}, failure(500, "session_renewal_invalid")
	}
	return renewed, after, nil
}
