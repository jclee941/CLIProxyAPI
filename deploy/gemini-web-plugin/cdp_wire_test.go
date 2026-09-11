package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const fakeGaia = "111111111111111111111"
const otherFakeGaia = "222222222222222222222"

type fakeCDPTarget struct {
	ID       string    `json:"targetId"`
	Type     string    `json:"type"`
	URL      string    `json:"url"`
	Context  string    `json:"browserContextId,omitempty"`
	Identity [3]string `json:"-"`
}

type fakeCDP struct {
	targets      []fakeCDPTarget
	afterCookies func(*fakeCDPTarget)
	errorMethod  string
	holdMethod   string
	entered      chan struct{}
	closed       chan struct{}
	cookies      atomic.Int32
	attached     atomic.Int32
	detached     atomic.Int32
	cookieValue  string
	infoContext  string
	rawReply     map[string]string
}

func fakeTarget(id, contextID, gaia string) fakeCDPTarget {
	return fakeCDPTarget{ID: id, Type: "page", URL: "https://gemini.google.com/app", Context: contextID, Identity: [3]string{gaia, gaia, gaia}}
}

func fakeCaptureRequest(authUser uint64) captureRequest {
	digest := sha256.Sum256([]byte(fakeGaia))
	binding := maintenanceSource{ExpectedGaiaSHA256: hex.EncodeToString(digest[:])}
	otherDigest := sha256.Sum256([]byte(otherFakeGaia))
	return captureRequest{Binding: binding, AuthUser: authUser, Bindings: map[string]maintenanceSource{
		"one": binding, "two": {ExpectedGaiaSHA256: hex.EncodeToString(otherDigest[:])},
	}}
}

func (fake *fakeCDP) source(t *testing.T) credentialSource {
	t.Helper()
	fake.closed = make(chan struct{}, 16)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Error("non-read-only HTTP method")
			writer.WriteHeader(405)
			return
		}
		switch request.URL.Path {
		case "/json/version":
			if err := json.NewEncoder(writer).Encode(map[string]string{"webSocketDebuggerUrl": "ws://" + request.Host + "/devtools/browser/test"}); err != nil {
				t.Error("version write failed")
			}
		case "/devtools/browser/test":
			upgrader := websocket.Upgrader{}
			connection, err := upgrader.Upgrade(writer, request, nil)
			if err != nil {
				t.Error("upgrade failed")
				return
			}
			defer func() { fake.closed <- struct{}{} }()
			defer connection.Close()
			fake.serve(t, connection)
		default:
			t.Error("unexpected HTTP path")
			writer.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	source := newCDPCredentialSource().(*cdpCredentialSource)
	source.origin = server.URL
	return source
}

func (fake *fakeCDP) serve(t *testing.T, connection *websocket.Conn) {
	sessions := make(map[string]int)
	for {
		var command struct {
			ID      int    `json:"id"`
			Method  string `json:"method"`
			Session string `json:"sessionId"`
			Params  struct {
				Target        string   `json:"targetId"`
				Session       string   `json:"sessionId"`
				Flatten       bool     `json:"flatten"`
				Expression    string   `json:"expression"`
				ReturnByValue bool     `json:"returnByValue"`
				URLs          []string `json:"urls"`
			} `json:"params"`
		}
		if err := connection.ReadJSON(&command); err != nil {
			return
		}
		if raw, ok := fake.rawReply[command.Method]; ok {
			if err := connection.WriteMessage(websocket.TextMessage, []byte(raw)); err != nil {
				return
			}
			continue
		}
		if command.Method == fake.holdMethod {
			close(fake.entered)
			for {
				if _, _, err := connection.ReadMessage(); err != nil {
					return
				}
			}
		}
		if command.Method == fake.errorMethod {
			if err := connection.WriteJSON(map[string]any{"id": command.ID, "sessionId": command.Session, "error": map[string]any{"code": -32001, "message": "secret-cookie-private-gaia"}}); err != nil {
				return
			}
			continue
		}
		var result any
		switch command.Method {
		case "Target.getTargets":
			result = map[string]any{"targetInfos": fake.targets}
		case "Target.attachToTarget":
			if !command.Params.Flatten || command.Session != "" {
				t.Error("attach must be flat and browser scoped")
			}
			found := false
			for index, target := range fake.targets {
				if target.ID == command.Params.Target {
					sessions["session-"+target.ID] = index
					found = true
				}
			}
			if !found {
				t.Error("attached unknown target")
				return
			}
			fake.attached.Add(1)
			result = map[string]string{"sessionId": "session-" + command.Params.Target}
		case "Runtime.evaluate":
			index, ok := sessions[command.Session]
			if !ok {
				t.Error("identity read outside attached session")
				return
			}
			if command.Params.Expression != `(() => ({origin: location.origin, S06Grb: window.WIZ_global_data?.S06Grb, W3Yyqf: window.WIZ_global_data?.W3Yyqf, qDCSke: window.WIZ_global_data?.qDCSke}))()` || !command.Params.ReturnByValue {
				t.Error("unexpected runtime expression")
			}
			target := fake.targets[index]
			origin := "https://gemini.google.com"
			if strings.HasPrefix(target.URL, "https://accounts.google.com") {
				origin = "https://accounts.google.com"
			}
			result = map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"origin": origin, "S06Grb": target.Identity[0], "W3Yyqf": target.Identity[1], "qDCSke": target.Identity[2]}}}
		case "Target.getTargetInfo":
			index, ok := sessions[command.Session]
			if !ok {
				t.Error("target info not bound to session")
				return
			}
			target := fake.targets[index]
			if fake.infoContext != "" {
				target.Context = fake.infoContext
			}
			result = map[string]any{"targetInfo": target}
		case "Network.getCookies":
			index, ok := sessions[command.Session]
			if !ok {
				t.Error("cookies outside target session")
				return
			}
			wantURL := "https://gemini.google.com/app"
			if strings.Contains(fake.targets[index].URL, "/u/1/") {
				wantURL = "https://gemini.google.com/u/1/app"
			}
			if len(command.Params.URLs) != 1 || command.Params.URLs[0] != wantURL {
				t.Error("cookie URL escaped Gemini account scope")
			}
			fake.cookies.Add(1)
			value := fake.cookieValue
			if value == "" {
				value = "synthetic-session"
			}
			result = map[string]any{"cookies": []map[string]string{{"name": "SID", "value": value}}}
			if fake.afterCookies != nil {
				fake.afterCookies(&fake.targets[index])
			}
		case "Target.detachFromTarget":
			if _, ok := sessions[command.Params.Session]; !ok {
				t.Error("detach unknown session")
			}
			delete(sessions, command.Params.Session)
			fake.detached.Add(1)
			result = struct{}{}
		default:
			t.Error("forbidden CDP method")
			return
		}
		if err := connection.WriteJSON(map[string]any{"id": command.ID, "sessionId": command.Session, "result": result}); err != nil {
			return
		}
	}
}

func awaitFakeClose(t *testing.T, fake *fakeCDP) {
	t.Helper()
	select {
	case <-fake.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("source socket leaked")
	}
}

func TestCDPSourceCloses_whenCancelledDuringIdentity(t *testing.T) {
	fake := &fakeCDP{targets: []fakeCDPTarget{fakeTarget("tab", "context", fakeGaia)}, holdMethod: "Runtime.evaluate", entered: make(chan struct{})}
	source := fake.source(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := source.Capture(ctx, fakeCaptureRequest(0)); result <- err }()
	select {
	case <-fake.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("identity not reached")
	}

	cancel()

	select {
	case err := <-result:
		assertCDPError(t, err, "source_cancelled")
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation blocked")
	}
	awaitFakeClose(t, fake)
	if fake.cookies.Load() != 0 {
		t.Fatal("read cookies after cancellation")
	}
}

func TestCDPSourceRejectsDiscovery_whenOriginOrProtocolIsUnsafe(t *testing.T) {
	for _, scenario := range []struct {
		name, body, location, code string
		status                     int
	}{
		{"foreign-websocket", `{"webSocketDebuggerUrl":"ws://foreign.invalid:9222/devtools/browser/private"}`, "", "source_unavailable", 200},
		{"userinfo", `{"webSocketDebuggerUrl":"ws://private@HOST/devtools/browser/test"}`, "", "source_unavailable", 200},
		{"page-websocket", `{"webSocketDebuggerUrl":"ws://HOST/devtools/page/test"}`, "", "source_unavailable", 200},
		{"query", `{"webSocketDebuggerUrl":"ws://HOST/devtools/browser/test?secret=value"}`, "", "source_unavailable", 200},
		{"redirect", "private-body", "/should-not-follow", "source_unavailable", 302},
		{"http-error", "private-body", "", "source_unavailable", 500},
		{"oversized", strings.Repeat("x", cdpMessageLimit+1), "", "source_protocol_failed", 200},
		{"malformed", "private-invalid-json", "", "source_protocol_failed", 200},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var calls, dials atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.URL.Path != "/json/version" {
					t.Error("followed redirect")
				}
				if scenario.location != "" {
					writer.Header().Set("Location", scenario.location)
				}
				writer.WriteHeader(scenario.status)
				if _, err := writer.Write([]byte(strings.ReplaceAll(scenario.body, "HOST", request.Host))); err != nil {
					return
				}
			}))
			defer server.Close()
			source := newCDPCredentialSource().(*cdpCredentialSource)
			source.origin = server.URL
			source.dialer = &websocket.Dialer{NetDialContext: func(context.Context, string, string) (net.Conn, error) {
				dials.Add(1)
				return nil, failure(503, "source_unavailable")
			}}

			_, err := source.Capture(t.Context(), fakeCaptureRequest(0))

			assertCDPError(t, err, scenario.code)
			if calls.Load() != 1 || dials.Load() != 0 {
				t.Fatal("unsafe discovery reached redirect or websocket dial")
			}
		})
	}
}

func TestCDPSourceFailsSafely_whenProtocolReplyIsInvalid(t *testing.T) {
	for _, scenario := range []struct {
		name string
		fake *fakeCDP
	}{
		{"unknown-error", &fakeCDP{errorMethod: "Runtime.evaluate"}},
		{"malformed-reply", &fakeCDP{rawReply: map[string]string{"Runtime.evaluate": "private-cookie-not-json"}}},
		{"oversized-reply", &fakeCDP{rawReply: map[string]string{"Runtime.evaluate": strings.Repeat("x", cdpMessageLimit+1)}}},
		{"wrong-request-id", &fakeCDP{rawReply: map[string]string{"Runtime.evaluate": `{"id":9999,"result":{}}`}}},
		{"wrong-session", &fakeCDP{rawReply: map[string]string{"Runtime.evaluate": `{"id":4,"sessionId":"other-session","result":{}}`}}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fake := scenario.fake
			fake.targets = []fakeCDPTarget{fakeTarget("tab", "context", fakeGaia)}
			source := fake.source(t)

			captured, err := source.Capture(t.Context(), fakeCaptureRequest(0))

			assertCDPError(t, err, "source_protocol_failed")
			awaitFakeClose(t, fake)
			if captured.Token.value != "" || fake.cookies.Load() != 0 {
				t.Fatal("unsafe protocol yielded cookies")
			}
		})
	}
}

func TestCDPSourceDefaultsRemainFixedAndCredentialBounded(t *testing.T) {
	source := newCDPCredentialSource().(*cdpCredentialSource)

	if source.origin != profileCDPOrigin || source.client.Timeout != 20*time.Second || source.dialer.Proxy != nil || source.dialer.HandshakeTimeout != 20*time.Second {
		t.Fatal("fixed credential transport defaults changed")
	}
	transport, ok := source.client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil || !transport.DisableKeepAlives {
		t.Fatal("transport may leak through environment proxy or remain open")
	}
}

func TestCDPSourceBoundsCredentialContext_whenParentHasLongerBudget(t *testing.T) {
	fake := &fakeCDP{}
	source := fake.source(t).(*cdpCredentialSource)
	var observed time.Time
	source.dialer = &websocket.Dialer{NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		var ok bool
		observed, ok = ctx.Deadline()
		if !ok {
			t.Error("credential connection is unbounded")
		}
		return nil, failure(503, "source_unavailable")
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	started := time.Now()

	_, err := source.Capture(ctx, fakeCaptureRequest(0))

	assertCDPError(t, err, "source_unavailable")
	if observed.IsZero() || observed.After(time.Now().Add(time.Minute)) || observed.Before(started.Add(59*time.Second)) {
		t.Fatal("credential context exceeded or lost its budget")
	}
}

func TestCDPSourcePreservesEarlierDeadline_whenCallerBudgetIsShorter(t *testing.T) {
	fake := &fakeCDP{}
	source := fake.source(t).(*cdpCredentialSource)
	var observed time.Time
	source.dialer = &websocket.Dialer{NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		observed, _ = ctx.Deadline()
		return nil, failure(503, "source_unavailable")
	}}
	deadline := time.Now().Add(10 * time.Second)
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()

	_, err := source.Capture(ctx, fakeCaptureRequest(0))

	assertCDPError(t, err, "source_unavailable")
	if !observed.Equal(deadline) {
		t.Fatal("caller deadline was extended")
	}
}
