package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type memorySecrets struct {
	mu     sync.Mutex
	tokens map[string]sessionToken
	reads  []string
	writes int
}

func (store *memorySecrets) Resolve(_ context.Context, reference secretReference) (sessionToken, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.reads = append(store.reads, reference.value)
	token, exists := store.tokens[reference.value]
	if !exists {
		return sessionToken{}, failure(503, "secret_store_unavailable")
	}
	return token, nil
}

func (store *memorySecrets) Put(_ context.Context, request secretWrite) (secretReference, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.writes++
	reference := request.Existing
	if reference.value == "" {
		reference = secretReference{item: strings.Repeat("a", 26), value: "op://homelab/" + strings.Repeat("a", 26) + "/web-session"}
	}
	store.tokens[reference.value] = request.Token
	return reference, nil
}

func (store *memorySecrets) ReplaceIfExpected(_ context.Context, request secretReplacement) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.tokens[request.Reference.value] != request.Expected {
		return failure(409, "credential_changed")
	}
	store.writes++
	store.tokens[request.Reference.value] = request.Replacement
	return nil
}

func TestMemorySecretStoreIsolatesReferences_whenUpdated(t *testing.T) {
	store := &memorySecrets{tokens: make(map[string]sessionToken)}
	first, err := store.Put(t.Context(), secretWrite{Token: sessionToken{"test-one"}})
	if err != nil {
		t.Fatal(err)
	}
	second := secretReference{value: "op://homelab/" + strings.Repeat("b", 26) + "/web-session"}
	if _, err := store.Put(t.Context(), secretWrite{Token: sessionToken{"test-two"}, Existing: second}); err != nil {
		t.Fatal(err)
	}
	result, err := store.Resolve(t.Context(), first)
	if err != nil || result.value != "test-one" {
		t.Fatal("secret references were mixed")
	}
}

func localSidecar(t *testing.T, service *service, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	transport := service.client.Transport.(*http.Transport)
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	t.Cleanup(service.client.CloseIdleConnections)
}

func writeFixture(t *testing.T, writer http.ResponseWriter, body string) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if _, err := io.WriteString(writer, body); err != nil {
		t.Error(err)
	}
}

func recordFixture(t *testing.T, letter string) storageRecord {
	t.Helper()
	return storageRecord{Type: provider, ID: "gemini-web-" + letter + ".json", Label: "Test " + letter, TokenRef: "op://homelab/" + strings.Repeat(letter, 26) + "/web-session"}
}

func jsonFixture(t *testing.T, value interface{}) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRegistrationPersistsOnlyReference_whenAccountVerified(t *testing.T) {
	var saved callbackRequest
	service := newService(func(method string, raw []byte) ([]byte, error) {
		if method != "host.auth.save" {
			t.Fatalf("unexpected callback %s", method)
		}
		if err := json.Unmarshal(raw, &saved); err != nil {
			t.Fatal(err)
		}
		return []byte(`{"ok":true,"result":{"name":"saved.json"}}`), nil
	})
	store := &memorySecrets{tokens: make(map[string]sessionToken)}
	service.secrets = store
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/account-models" || request.Header.Get("x-goog-api-key") != encodedToken("test-cookie") {
			t.Error("registration did not verify submitted token")
		}
		writeFixture(t, writer, `{"available":true,"models":[{"capability_id":"test-flash","display_name":"3.8 Flash","mode":1}]}`)
	})
	result := invoke(t, service, "management.handle", managementRequest{Method: "POST", Path: accountsPath, HostCallbackID: "scope-register", Body: jsonFixture(t, struct{ Label, Token string }{"<Test Label>", encodedToken("test-cookie")})})
	var response httpResponse
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || store.writes != 1 || saved.HostCallbackID != "scope-register" {
		t.Fatalf("registration failed: %s", response.Body)
	}
	var storage map[string]json.RawMessage
	if err := json.Unmarshal(saved.JSON, &storage); err != nil {
		t.Fatal(err)
	}
	for field := range storage {
		switch field {
		case "type", "id", "label", "token_ref", "disabled", "request_scoped_errors", "session_revision":
		default:
			t.Fatalf("unexpected auth storage field %s", field)
		}
	}
	record, err := service.parseStorage(saved.JSON, true)
	if record.SessionRevision != 1 {
		t.Fatal("new account registration must publish its initial non-secret revision")
	}
	if err != nil || record.Type != provider || record.ID != saved.Name || record.Label != "<Test Label>" || record.TokenRef != recordFixture(t, "a").TokenRef {
		t.Fatal("stored reference identity changed")
	}
	if strings.Contains(string(saved.JSON), "test-cookie") || strings.Contains(string(saved.JSON), encodedToken("test-cookie")) {
		t.Fatal("raw token persisted")
	}
	assertCanonicalStopPolicy(t, genericMetadataFromSavedJSON(t, saved.JSON).RequestScopedErrors)
	if strings.Contains(string(response.Body), "op://") || strings.Contains(string(response.Body), "gemini-web:v1:") {
		t.Fatal("registration leaked credential")
	}
}

func TestRegistrationRejectsGuest_beforePersisting(t *testing.T) {
	service := newService(func(string, []byte) ([]byte, error) { t.Fatal("guest reached host storage"); return nil, nil })
	store := &memorySecrets{tokens: make(map[string]sessionToken)}
	service.secrets = store
	localSidecar(t, service, func(writer http.ResponseWriter, _ *http.Request) {
		writeFixture(t, writer, `{"available":false,"models":[]}`)
	})
	result := invoke(t, service, "management.handle", managementRequest{Method: "POST", Path: accountsPath, Body: jsonFixture(t, struct{ Label, Token string }{"Guest", encodedToken("test-guest")})})
	var response httpResponse
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 401 || store.writes != 0 {
		t.Fatal("guest account accepted")
	}
}

func TestOmniDoesNotResubmit_whenSidecarReturnsFailure(t *testing.T) {
	service := newService(nil)
	record := recordFixture(t, "a")
	auth, err := authFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	service.secrets = &memorySecrets{tokens: map[string]sessionToken{record.TokenRef: {encodedToken("test-omni")}}}
	var lock sync.Mutex
	calls := 0
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/session/renew" {
			writeFixture(t, writer, `{"token":"`+encodedToken("test-omni")+`"}`)
			return
		}
		lock.Lock()
		calls++
		lock.Unlock()
		writer.WriteHeader(502)
		writeFixture(t, writer, `{"error":"untrusted-secret"}`)
	})
	result := invoke(t, service, "executor.execute", executorRequest{AuthID: record.ID, AuthProvider: provider, Model: omniModel, Format: "gemini", SourceFormat: "gemini", Payload: []byte(`{"contents":[{"parts":[{"text":"test video"}]}]}`), StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata})
	lock.Lock()
	defer lock.Unlock()
	if result.OK || result.Error.HTTPStatus != 502 || calls != 1 || !strings.HasPrefix(result.Error.Message, "gemini_web_omni:") || strings.Contains(result.Error.Message, "untrusted-secret") {
		t.Fatalf("unsafe retry/error: calls=%d error=%+v", calls, result.Error)
	}
}

func TestDirectTransportDoesNotFollowRedirects_orEnvironmentProxy(t *testing.T) {
	service := newService(nil)
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	calls := 0
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		calls++
		writer.Header().Set("Location", sidecarBase+"/v1/usage")
		writer.WriteHeader(307)
	})
	_, err := service.sidecar(t.Context(), sidecarRequest{Method: "GET", Path: "/v1/account-models", Token: sessionToken{encodedToken("test-redirect")}})
	if err == nil || calls != 1 || service.client.Transport.(*http.Transport).Proxy != nil || service.client.Timeout != 0 {
		t.Fatal("direct/redirect transport boundary failed")
	}
}

func TestPinnedAuthRejectsMismatch_beforeResolvingCredential(t *testing.T) {
	service := newService(nil)
	store := &memorySecrets{tokens: make(map[string]sessionToken)}
	service.secrets = store
	request := executorRequest{AuthID: "gemini-web-a.json", AuthProvider: provider, Model: flashModel, Format: "gemini", Payload: []byte(`{}`)}
	request.Metadata.PinnedAuthID = "gemini-web-b.json"
	result := invoke(t, service, "executor.execute", request)
	if result.OK || len(store.reads) != 0 {
		t.Fatal("pinned account fell back")
	}
}
