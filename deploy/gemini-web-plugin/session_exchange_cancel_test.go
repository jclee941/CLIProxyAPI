package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type exchangePending struct {
	ctx     context.Context
	release chan struct{}
}

type exchangeCancelFixture struct {
	*exchangeFixture
	started chan exchangePending
	reading chan struct{}
}

func newExchangeCancelFixture(t *testing.T, responseBody bool) *exchangeCancelFixture {
	t.Helper()
	fixture := &exchangeCancelFixture{started: make(chan exchangePending, 4), reading: make(chan struct{}, 1)}
	fixture.exchangeFixture = newExchangeFixture(t, 2, func(writer http.ResponseWriter, request *http.Request) {
		if responseBody {
			writer.Header().Set("Content-Length", "100")
			writer.Header().Add("Set-Cookie", "SID=rotated; Domain=google.com; Path=/; Secure")
			writer.Header().Add("Set-Cookie", "OSID=rotated-flow; Path=/; Secure")
			if _, err := writer.Write([]byte("partial")); err != nil {
				return
			}
			if err := http.NewResponseController(writer).Flush(); err != nil {
				t.Error(err)
				return
			}
		}
		pending := exchangePending{ctx: request.Context(), release: make(chan struct{})}
		fixture.started <- pending
		select {
		case <-request.Context().Done():
		case <-pending.release:
			exchangeOK(writer, request)
		case <-t.Context().Done():
		}
	})
	t.Cleanup(func() {
		fixture.server.CloseClientConnections()
		fixture.service.stop()
	})
	if responseBody {
		fixture.service.client.Transport = exchangeBodyTransport{base: fixture.service.client.Transport, reading: fixture.reading}
	}
	return fixture
}

// These wrappers observe the real HTTP response's first body read without
// substituting its transport, body, or cancellation behavior.
type exchangeBodyTransport struct {
	base    http.RoundTripper
	reading chan<- struct{}
}

func (transport exchangeBodyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err == nil {
		response.Body = exchangeBodyReader{ReadCloser: response.Body, reading: transport.reading}
	}
	return response, err
}

type exchangeBodyReader struct {
	io.ReadCloser
	reading chan<- struct{}
}

func (body exchangeBodyReader) Read(buffer []byte) (int, error) {
	select {
	case body.reading <- struct{}{}:
	default:
	}
	return body.ReadCloser.Read(buffer)
}

func awaitExchangeSignal[T any](t *testing.T, signal <-chan T) T {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case value := <-signal:
		return value
	case <-timer.C:
		t.Fatal("exchange signal did not arrive")
		var zero T
		return zero
	}
}

func startExchangeManagement(t *testing.T, service *service, request managementRequest) <-chan []byte {
	t.Helper()
	raw := jsonFixture(t, request)
	done := make(chan []byte, 1)
	// The native ABI supplies a detached context, not the broker HTTP context.
	go func() { done <- service.handle(context.Background(), "management.handle", raw) }()
	return done
}

func exchangeManagementResponse(t *testing.T, done <-chan []byte) httpResponse {
	t.Helper()
	var envelope envelope
	if err := json.Unmarshal(awaitExchangeSignal(t, done), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatalf("management envelope failed: %+v", envelope.Error)
	}
	var response httpResponse
	if err := json.Unmarshal(envelope.Result, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func requestExchangeCancel(t *testing.T, service *service, key sessionExchangeKey) bool {
	t.Helper()
	response := exchangeManagementResponse(t, startExchangeManagement(t, service, managementRequest{
		Method: "POST", Path: sessionExchangeCancelPath, HostCallbackID: "scope-list", Body: jsonFixture(t, key),
	}))
	var result struct {
		Requested bool `json:"requested"`
		Active    bool `json:"active"`
	}
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !result.Requested {
		t.Fatalf("cancel = %d %s", response.StatusCode, response.Body)
	}
	return result.Active
}

func expectCancelledExchange(t *testing.T, done <-chan []byte) {
	t.Helper()
	response := exchangeManagementResponse(t, done)
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 499 || body.Error != "session_exchange_cancelled" {
		t.Fatalf("exchange = %d %s", response.StatusCode, response.Body)
	}
}

func TestSessionExchangeCancelReachesUpstreamContext(t *testing.T) {
	for _, responseBody := range []bool{false, true} {
		name := "before_headers"
		if responseBody {
			name = "reading_body"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newExchangeCancelFixture(t, responseBody)
			key := sessionExchangeKey{AuthID: fixture.record.ID, RequestID: "flow_1-abc"}
			request := sessionExchangeRequest{AuthID: key.AuthID, RequestID: key.RequestID, Method: "GET", URL: "https://flow.google.com/projects"}
			done := startExchangeManagement(t, fixture.service, managementRequest{
				Method: "POST", Path: sessionExchangePath, HostCallbackID: "scope-list", Body: jsonFixture(t, request),
			})
			pending := awaitExchangeSignal(t, fixture.started)
			if responseBody {
				awaitExchangeSignal(t, fixture.reading)
			}

			if !requestExchangeCancel(t, fixture.service, key) {
				t.Fatal("active exchange was not found")
			}

			awaitExchangeSignal(t, pending.ctx.Done())
			if !errors.Is(pending.ctx.Err(), context.Canceled) {
				t.Fatalf("upstream context = %v", pending.ctx.Err())
			}
			expectCancelledExchange(t, done)
			if requestExchangeCancel(t, fixture.service, key) {
				t.Fatal("completed cancellation left an active exchange")
			}
			if responseBody {
				credential := storedCredential(t, fixture.exchangeFixture)
				if credential.Cookie != "SID=rotated; SAPISID=secret" || credential.FlowCookies["OSID"] != "rotated-flow" {
					t.Fatal("cancellation discarded the received cookie rotation")
				}
			}
		})
	}
}

func TestSessionExchangeCancelScopesAccountAndRequestID(t *testing.T) {
	fixture := newExchangeCancelFixture(t, false)
	other := recordFixture(t, "b")
	fixture.service.config.SessionExchangeAccounts = append(fixture.service.config.SessionExchangeAccounts, other.ID)
	keys := []sessionExchangeKey{
		{AuthID: fixture.record.ID, RequestID: "shared"},
		{AuthID: other.ID, RequestID: "shared"},
		{AuthID: fixture.record.ID, RequestID: "other"},
	}
	var done []<-chan []byte
	var pending []exchangePending
	for _, key := range keys {
		request := sessionExchangeRequest{AuthID: key.AuthID, RequestID: key.RequestID, Method: "GET", URL: "https://flow.google.com/projects"}
		done = append(done, startExchangeManagement(t, fixture.service, managementRequest{
			Method: "POST", Path: sessionExchangePath, HostCallbackID: "scope-list", Body: jsonFixture(t, request),
		}))
		pending = append(pending, awaitExchangeSignal(t, fixture.started))
	}

	if !requestExchangeCancel(t, fixture.service, keys[0]) {
		t.Fatal("active account/request pair was not found")
	}

	awaitExchangeSignal(t, pending[0].ctx.Done())
	expectCancelledExchange(t, done[0])
	for index := 1; index < len(keys); index++ {
		if err := pending[index].ctx.Err(); err != nil {
			t.Fatalf("unrelated exchange %d was cancelled: %v", index, err)
		}
		close(pending[index].release)
		if response := exchangeManagementResponse(t, done[index]); response.StatusCode != 200 {
			t.Fatalf("unrelated exchange = %d %s", response.StatusCode, response.Body)
		}
		if requestExchangeCancel(t, fixture.service, keys[index]) {
			t.Fatal("normal completion left an active exchange")
		}
	}
}

func TestSessionExchangeCancelRequiresHostScopeAndAllowlistedAccount(t *testing.T) {
	fixture := newExchangeFixture(t, 2, exchangeOK)
	host := fixture.service.host
	fixture.service.host = func(method string, raw []byte) ([]byte, error) {
		var request callbackRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if request.HostCallbackID != "scope-list" {
			return nil, failure(403, "callback_denied")
		}
		return host(method, raw)
	}
	for _, test := range []struct {
		name, authID, callback string
		status                 int
	}{
		{"unlisted", "gemini-web-b.json", "scope-list", 403},
		{"unknown", "gemini-web-d.json", "scope-list", 404},
		{"missing callback", fixture.record.ID, "", 503},
		{"foreign callback", fixture.record.ID, "scope-other", 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := exchangeManagementResponse(t, startExchangeManagement(t, fixture.service, managementRequest{
				Method: "POST", Path: sessionExchangeCancelPath, HostCallbackID: test.callback,
				Body: jsonFixture(t, sessionExchangeKey{AuthID: test.authID, RequestID: "request"}),
			}))
			if response.StatusCode != test.status {
				t.Fatalf("cancel = %d %s, want %d", response.StatusCode, response.Body, test.status)
			}
		})
	}
	for _, body := range []string{`{`, `null`, `{}`, `{"auth_id":"gemini-web-a.json"}`, `{"request_id":"request"}`, `{"auth_id":"","request_id":"request"}`, `{"auth_id":"gemini-web-a.json","request_id":"request","extra":true}`} {
		response := exchangeManagementResponse(t, startExchangeManagement(t, fixture.service, managementRequest{
			Method: "POST", Path: sessionExchangeCancelPath, HostCallbackID: "scope-list", Body: []byte(body),
		}))
		if response.StatusCode != 400 {
			t.Fatalf("body %s: cancel = %d %s", body, response.StatusCode, response.Body)
		}
	}
	if requestExchangeCancel(t, fixture.service, sessionExchangeKey{AuthID: fixture.record.ID, RequestID: "absent"}) {
		t.Fatal("absent exchange reported active")
	}
	if len(fixture.requests()) != 0 {
		t.Fatal("cancel endpoint forwarded an upstream request")
	}
}

func TestSessionExchangeCancelDoesNotAcquireCredentialGuard(t *testing.T) {
	fixture := newExchangeFixture(t, 2, exchangeOK)
	key := sessionExchangeKey{AuthID: fixture.record.ID, RequestID: "before_lease"}
	ctx, finish, err := fixture.service.beginSessionExchange(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	lease, err := fixture.service.accountLease(fixture.record)
	if err != nil {
		t.Fatal(err)
	}
	lease.guard.Lock()
	defer lease.guard.Unlock()
	lease.set(credentialState{state: maintenanceFenced, nextDue: fixture.service.now().Add(time.Hour)})

	if !requestExchangeCancel(t, fixture.service, key) {
		t.Fatal("active exchange was hidden by the credential guard")
	}

	awaitExchangeSignal(t, ctx.Done())
}

func TestSessionExchangeCancelRegistrationAndMethod(t *testing.T) {
	service := newService(nil)
	registered := invoke(t, service, "management.register", struct{}{})
	var registration struct {
		Routes []struct{ Method, Path string }
	}
	if !registered.OK || json.Unmarshal(registered.Result, &registration) != nil {
		t.Fatalf("registration failed: %+v", registered.Error)
	}
	found := false
	for _, route := range registration.Routes {
		if route.Method == "POST" && "/v0/management"+route.Path == sessionExchangeCancelPath {
			found = true
		}
	}
	if !found {
		t.Fatal("cancel management route was not registered")
	}
	response := exchangeManagementResponse(t, startExchangeManagement(t, service, managementRequest{Method: "GET", Path: sessionExchangeCancelPath}))
	if response.StatusCode != 404 {
		t.Fatalf("GET cancel = %d", response.StatusCode)
	}
}

func TestSessionExchangeRequestIDValidation(t *testing.T) {
	fixture := newExchangeFixture(t, 2, exchangeOK)
	for _, value := range []any{"", nil, true, 123, []string{"x"}, "a b", "a/b", "a.b", "a\n", "\u00e9", strings.Repeat("a", 129)} {
		for _, path := range []string{sessionExchangePath, sessionExchangeCancelPath} {
			body := map[string]any{"auth_id": fixture.record.ID, "request_id": value}
			if path == sessionExchangePath {
				body["method"], body["url"] = "GET", "https://flow.google.com/projects"
			}
			response := exchangeManagementResponse(t, startExchangeManagement(t, fixture.service, managementRequest{
				Method: "POST", Path: path, HostCallbackID: "scope-list", Body: jsonFixture(t, body),
			}))
			if response.StatusCode != 400 {
				t.Fatalf("%s request_id=%#v: %d %s", path, value, response.StatusCode, response.Body)
			}
		}
	}
	if len(fixture.requests()) != 0 {
		t.Fatal("invalid ID reached upstream")
	}
	for _, id := range []sessionExchangeRequestID{"a", "AZaz09_-", sessionExchangeRequestID(strings.Repeat("a", 128))} {
		if _, err := fixture.exchange(t, sessionExchangeRequest{AuthID: fixture.record.ID, RequestID: id, Method: "GET", URL: "https://flow.google.com/projects"}); err != nil {
			t.Fatalf("valid request_id %q: %v", id, err)
		}
	}
}

func TestSessionExchangeRejectsActiveDuplicateAndReusesAfterCancellation(t *testing.T) {
	fixture := newExchangeCancelFixture(t, false)
	key := sessionExchangeKey{AuthID: fixture.record.ID, RequestID: "duplicate"}
	request := managementRequest{
		Method: "POST", Path: sessionExchangePath, HostCallbackID: "scope-list",
		Body: jsonFixture(t, sessionExchangeRequest{AuthID: key.AuthID, RequestID: key.RequestID, Method: "GET", URL: "https://flow.google.com/projects"}),
	}
	done := startExchangeManagement(t, fixture.service, request)
	pending := awaitExchangeSignal(t, fixture.started)

	response := exchangeManagementResponse(t, startExchangeManagement(t, fixture.service, request))

	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 409 || body.Error != "session_exchange_request_active" || len(fixture.requests()) != 1 {
		t.Fatalf("duplicate = %d %s; forwarded requests = %d", response.StatusCode, response.Body, len(fixture.requests()))
	}
	if !requestExchangeCancel(t, fixture.service, key) {
		t.Fatal("duplicate removed the original cancellation")
	}
	awaitExchangeSignal(t, pending.ctx.Done())
	expectCancelledExchange(t, done)
	done = startExchangeManagement(t, fixture.service, request)
	pending = awaitExchangeSignal(t, fixture.started)
	close(pending.release)
	if response := exchangeManagementResponse(t, done); response.StatusCode != 200 {
		t.Fatalf("ID reuse = %d %s", response.StatusCode, response.Body)
	}
	if requestExchangeCancel(t, fixture.service, key) {
		t.Fatal("normal completion left the reused ID active")
	}
}

func TestSessionExchangeWithoutIDRemainsBufferedAndConcurrent(t *testing.T) {
	fixture := newExchangeCancelFixture(t, false)
	key := sessionExchangeKey{AuthID: fixture.record.ID, RequestID: "not_yet_started"}
	if requestExchangeCancel(t, fixture.service, key) {
		t.Fatal("absent exchange reported active")
	}
	var done []<-chan []byte
	var pending []exchangePending
	for _, id := range []sessionExchangeRequestID{"", "", key.RequestID} {
		request := sessionExchangeRequest{AuthID: fixture.record.ID, RequestID: id, Method: "GET", URL: "https://flow.google.com/projects"}
		done = append(done, startExchangeManagement(t, fixture.service, managementRequest{
			Method: "POST", Path: sessionExchangePath, HostCallbackID: "scope-list", Body: jsonFixture(t, request),
		}))
		pending = append(pending, awaitExchangeSignal(t, fixture.started))
	}

	for index := range done {
		select {
		case response := <-done[index]:
			t.Fatalf("exchange returned before upstream completion: %s", response)
		default:
		}
		if err := pending[index].ctx.Err(); err != nil {
			t.Fatalf("exchange was cancelled without an active cancellation: %v", err)
		}
		close(pending[index].release)
		response := exchangeManagementResponse(t, done[index])
		var result sessionExchangeResult
		if err := json.Unmarshal(response.Body, &result); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 || result.StatusCode != 200 || string(result.Body) != "ok" {
			t.Fatalf("ordinary exchange = %d %s", response.StatusCode, response.Body)
		}
	}
}

func TestSessionExchangeShutdownCancelsBeforeDraining(t *testing.T) {
	for _, stop := range []bool{false, true} {
		name := "shutdown"
		if stop {
			name = "stop"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newExchangeCancelFixture(t, false)
			closing := fixture.service.lifecycle.closingSignal()
			var done []<-chan []byte
			var pending []exchangePending
			for _, id := range []sessionExchangeRequestID{"active", ""} {
				request := sessionExchangeRequest{AuthID: fixture.record.ID, RequestID: id, Method: "GET", URL: "https://flow.google.com/projects"}
				done = append(done, startExchangeManagement(t, fixture.service, managementRequest{
					Method: "POST", Path: sessionExchangePath, HostCallbackID: "scope-list", Body: jsonFixture(t, request),
				}))
				pending = append(pending, awaitExchangeSignal(t, fixture.started))
			}
			shutdown := make(chan error, 1)

			go func() {
				if stop {
					fixture.service.stop()
					shutdown <- nil
				} else {
					shutdown <- fixture.service.shutdownSessions()
				}
			}()

			awaitExchangeSignal(t, closing)
			for index := range done {
				awaitExchangeSignal(t, pending[index].ctx.Done())
				expectCancelledExchange(t, done[index])
			}
			if err := awaitExchangeSignal(t, shutdown); err != nil {
				t.Fatal(err)
			}
			fixture.service.sessionExchangesMu.Lock()
			active := len(fixture.service.sessionExchanges)
			fixture.service.sessionExchangesMu.Unlock()
			if active != 0 {
				t.Fatalf("shutdown left %d exchanges active", active)
			}
			lease, err := fixture.service.accountLease(fixture.record)
			if err != nil {
				t.Fatal(err)
			}
			if !lease.guard.TryLock() {
				t.Fatal("shutdown retained the exchange credential lease")
			}
			lease.guard.Unlock()
		})
	}
}

func TestSessionExchangeCleanupCancelsOwnedContext(t *testing.T) {
	service := newService(nil)
	key := sessionExchangeKey{AuthID: "gemini-web-a.json", RequestID: "normal"}
	ctx, finish, err := service.beginSessionExchange(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}

	finish()

	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("completed context = %v", ctx.Err())
	}
	next, finishNext, err := service.beginSessionExchange(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer finishNext()
	if err := next.Err(); err != nil {
		t.Fatalf("reused ID inherited cancellation: %v", err)
	}
}
