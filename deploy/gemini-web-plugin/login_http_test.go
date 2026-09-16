package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoginHTTPPortalSurface_whenAuthenticatedHandoffCompletes(t *testing.T) {
	service, host := loginFixture(t)
	portal := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer fixture-management" {
			writer.WriteHeader(401)
			return
		}
		body, err := io.ReadAll(io.LimitReader(request.Body, 40001))
		if err != nil {
			t.Error(err)
			writer.WriteHeader(400)
			return
		}
		raw, err := json.Marshal(managementRequest{Method: request.Method, Path: request.URL.Path, Query: request.URL.Query(), Headers: request.Header, Body: body, HostCallbackID: "http-fixture-" + request.URL.Path})
		if err != nil {
			t.Error(err)
			writer.WriteHeader(500)
			return
		}
		response, err := service.management(request.Context(), raw)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(500)
			return
		}
		for name, values := range response.Headers {
			writer.Header()[name] = values
		}
		writer.WriteHeader(response.StatusCode)
		if _, err := writer.Write(response.Body); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(portal.Close)
	post := func(operation, origin, authorization string, body []byte) (loginView, int) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), "POST", portal.URL+loginPath+operation, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Origin", origin)
		request.Header.Set("Authorization", authorization)
		request.Header.Set("Content-Type", "application/json")
		response, err := portal.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		raw, readErr := io.ReadAll(response.Body)
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		var view loginView
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &view); err != nil {
				t.Fatal(err)
			}
		}
		if response.StatusCode != 401 && response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("credential flow response was cacheable")
		}
		return view, response.StatusCode
	}
	startBody := []byte(`{"label":"HTTP fixture","consent":true}`)
	if _, status := post("start", "https://manager.example", "", startBody); status != 401 {
		t.Fatal("unauthenticated request accepted")
	}
	if _, status := post("start", "https://other.example", "Bearer fixture-management", startBody); status != 403 {
		t.Fatal("cross-origin request accepted")
	}
	started, status := post("start", "https://manager.example", "Bearer fixture-management", startBody)
	if status != 200 || started.Status != loginPending {
		t.Fatalf("start status=%d", status)
	}
	user := uint64(2)
	body := jsonFixture(t, loginCompletion{State: started.State, Token: encodedToken("test-login"), AccountSHA256: testAccountDigest, AuthUser: &user, ExtensionID: strings.Repeat("a", 32), Consent: true})

	ready, status := post("complete", "https://manager.example", "Bearer fixture-management", body)

	if status != 200 || ready.Status != loginReady || !ready.ModelsReady || host.models != 1 {
		t.Fatalf("HTTP handoff status=%d flow=%s models=%d", status, ready.Status, host.models)
	}
	for _, operation := range []string{"status", "cancel", "reconcile"} {
		view, status := post(operation, "https://manager.example", "Bearer fixture-management", jsonFixture(t, struct {
			State string `json:"state"`
		}{started.State}))
		if status != 200 || view.AccountID != ready.AccountID || view.Status != loginReady {
			t.Fatalf("%s changed committed account: %d %s", operation, status, view.Status)
		}
	}
	view, status := post("complete", "https://manager.example", "Bearer fixture-management", body)
	if status != 200 || view.Status != loginReady || host.saves != 1 {
		t.Fatal("matching replay repeated save")
	}
	record, enabled, err := service.findRecord("http-usage", ready.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	account := service.inspectAccount(t.Context(), record, enabled)
	if account.Status != "ready" || account.Usage == nil || account.Usage.Tier != nil || account.Usage.Metrics != nil {
		t.Fatal("unknown Google usage was invented or rejected")
	}
	t.Log("HTTP 401/403 boundaries, start/complete/status/cancel/reconcile, matching replay, synchronous model registration and nullable measured usage verified; zero Vault calls")
}

func TestLoginCapacityAndExpiry_whenThirtyTwoFlowsAreActive(t *testing.T) {
	service, _ := loginFixture(t)
	now := time.Unix(1800000000, 0)
	service.now = func() time.Time { return now }
	seen := make(map[string]bool)
	for range 32 {
		view, status := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
		if status != 200 || !loginStatePattern.MatchString(view.State) || seen[view.State] || view.ExpiresAt != now.Add(10*time.Minute).Unix() {
			t.Fatal("invalid state capacity, randomness or TTL")
		}
		seen[view.State] = true
	}

	view, status := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))

	if status != 429 || view.Error != "login_capacity_reached" {
		t.Fatal("active state cap not enforced")
	}
	now = now.Add(10 * time.Minute)
	if _, status := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`)); status != 200 {
		t.Fatal("expired states retained active slots")
	}
}

func TestLocalKeyAndReferenceParsersRejectUnsafeInputs_whenOpening(t *testing.T) {
	for _, key := range []string{"", "not-base64", base64.StdEncoding.EncodeToString(make([]byte, 31)), strings.TrimSuffix(sessionKeyFixture(), "="), sessionKeyFixture() + "\n"} {
		if store, err := openSessionStore(filepath.Join(t.TempDir(), "sessions"), key); err == nil {
			t.Error("invalid key accepted")
			if err := store.close(); err != nil {
				t.Error(err)
			}
		}
	}
	for _, reference := range []string{"session://gemini-web/../key", "session://gemini-web/" + strings.Repeat("a", 32) + "?x=1", "session://other/" + strings.Repeat("a", 32), "session://gemini-web/" + strings.Repeat("A", 32)} {
		if _, err := parseCredentialReference(reference); err == nil {
			t.Fatal("unsafe local reference accepted")
		}
	}
	parent := t.TempDir()
	link := filepath.Join(parent, "linked")
	if err := os.Symlink(parent, link); err != nil {
		t.Fatal(err)
	}
	if store, err := openSessionStore(link, sessionKeyFixture()); err == nil {
		t.Error("symlink session directory accepted")
		if err := store.close(); err != nil {
			t.Error(err)
		}
	}
}

func TestSessionEnvelopeUsesFreshNonce_whenOnlyStateChanges(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "sessions")
	store, err := openSessionStore(directory, sessionKeyFixture())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.close(); err != nil {
			t.Error(err)
		}
	})
	record := localRecordFixture(t)
	if err := store.write(record); err != nil {
		t.Fatal(err)
	}
	name, err := sessionFile(record.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		t.Fatal(err)
	}
	record.State = localReady

	if err := store.write(record); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		t.Fatal(err)
	}
	var first, second sessionEnvelope
	if err := json.Unmarshal(before, &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(after, &second); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Nonce, second.Nonce) || bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("state change reused authenticated encryption nonce")
	}
}
