package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// seedSessions gives the service an encrypted local session store already
// holding one ready session per reference. A credential lives in that store
// now, so this is what a fixture account needs to be usable.
func seedSessions(t *testing.T, service *service, tokens map[string]sessionToken) {
	t.Helper()
	if service.sessions == nil {
		store, err := openSessionStore(filepath.Join(t.TempDir(), "sessions"), sessionKeyFixture())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := store.close(); err != nil {
				t.Error(err)
			}
		})
		service.sessions = store
	}
	records := make([]storageRecord, 0, len(tokens))
	for reference, token := range tokens {
		record := referenceRecord(t, reference)
		records = append(records, record)
		seedSession(t, service, record, token)
	}
	// A local session renews through the host on every omni turn, so a seeded
	// fixture needs a host that can answer for exactly these records.
	if service.host == nil {
		service.host = accountHost(t, records)
	}
}

// referenceRecord rebuilds the record recordFixture produces for a reference,
// so a seeded session carries the same binding the caller passes to the host.
func referenceRecord(t *testing.T, reference string) storageRecord {
	t.Helper()
	letter := strings.TrimPrefix(reference, "session://gemini-web/")
	if len(letter) != 32 {
		t.Fatalf("unexpected session reference %q", reference)
	}
	return recordFixture(t, letter[:1])
}

func seedSession(t *testing.T, service *service, record storageRecord, token sessionToken) {
	t.Helper()
	auth, err := authFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	local := localSession{Target: record, Projection: string(auth.StorageJSON), Token: token.value, Identity: credentialInspection{AccountSHA256: testAccountDigest, AuthUser: 2}, State: localReady}
	if err := service.sessions.write(local); err != nil {
		t.Fatal(err)
	}
}

// The dial override sends every outbound call to this one server, so it has to
// answer the native credential routes as well as the sidecar ones; otherwise a
// test that only stubs the sidecar would let account and rotation calls fall
// through to the real product.
func localSidecar(t *testing.T, service *service, handler http.HandlerFunc) {
	t.Helper()
	localSidecarRotating(t, service, handler, false)
}

// localSidecarAll leaves the credential routes to the handler, for tests that
// decide what every call does - usually failing all of them.
func localSidecarAll(t *testing.T, service *service, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	transport := service.client.Transport.(*http.Transport)
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	service.webOriginOverride = server.URL
	service.webRotateOverride = server.URL
	t.Cleanup(service.client.CloseIdleConnections)
}

// A rotation that returns no cookies leaves the token untouched, which is what
// most tests assume; only the ones about replacement ask for a changed jar.
func localSidecarRotating(t *testing.T, service *service, handler http.HandlerFunc, rotate bool) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/RotateCookies":
			if rotate {
				http.SetCookie(writer, &http.Cookie{Name: "__Secure-1PSIDTS", Value: "rotated", Path: "/", Domain: ".google.com", Secure: true})
			}
			writer.WriteHeader(http.StatusOK)
		case strings.HasSuffix(request.URL.Path, "/app"):
			writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			if _, err := io.WriteString(writer, nativeIdentityPage(testGaia)); err != nil {
				t.Error(err)
			}
		default:
			handler(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	transport := service.client.Transport.(*http.Transport)
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	// Redirecting the dial is not enough for the native calls: their origins are
	// https, and this server speaks plain http.
	if service.webOriginOverride == "" {
		service.webOriginOverride = server.URL
	}
	if service.webRotateOverride == "" {
		service.webRotateOverride = server.URL
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
	return storageRecord{Type: provider, ID: "gemini-web-" + letter + ".json", Label: "Test " + letter, TokenRef: "session://gemini-web/" + strings.Repeat(letter, 32), SessionRevision: 1}
}

func jsonFixture(t *testing.T, value interface{}) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestOmniDoesNotResubmit_whenSidecarReturnsFailure(t *testing.T) {
	service := newService(nil)
	record := recordFixture(t, "a")
	auth, err := authFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	seedSessions(t, service, map[string]sessionToken{record.TokenRef: {encodedToken("test-omni")}})
	var lock sync.Mutex
	calls := 0
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		if sidecarPath(request) == "/v1/session/renew" {
			writeFixture(t, writer, `{"token":"`+encodedToken("test-omni")+`"}`)
			return
		}
		lock.Lock()
		calls++
		lock.Unlock()
		writer.WriteHeader(502)
		writeFixture(t, writer, `{"error":"untrusted-secret"}`)
	})
	result := invoke(t, service, "executor.execute", executorRequest{AuthID: record.ID, AuthProvider: provider, Model: omniModel, Format: "gemini", SourceFormat: "gemini", Payload: []byte(`{"contents":[{"parts":[{"text":"test video"}]}]}`), StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, HostCallbackID: "scope-list"})
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
	request := executorRequest{AuthID: "gemini-web-a.json", AuthProvider: provider, Model: flashModel, Format: "gemini", Payload: []byte(`{}`)}
	request.Metadata.PinnedAuthID = "gemini-web-b.json"
	result := invoke(t, service, "executor.execute", request)
	if result.OK {
		t.Fatal("pinned account fell back")
	}
}
