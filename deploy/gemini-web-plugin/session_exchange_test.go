package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type exchangeSeen struct {
	method, path, rawQuery string
	header                 http.Header
	body                   []byte
}

type exchangeFixture struct {
	server   *httptest.Server
	mu       sync.Mutex
	seen     []exchangeSeen
	service  *service
	record   storageRecord
	disabled storageRecord
}

func (fixture *exchangeFixture) requests() []exchangeSeen {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return append([]exchangeSeen(nil), fixture.seen...)
}

// newExchangeFixture configures account a (the credential under test), c
// (configured but disabled) and d (configured but unknown to the host); b is a
// real account the policy does not list.
func newExchangeFixture(t *testing.T, authUser int, reply func(http.ResponseWriter, *http.Request)) *exchangeFixture {
	t.Helper()
	fixture := &exchangeFixture{}
	fixture.server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := new(bytes.Buffer)
		if _, err := body.ReadFrom(request.Body); err != nil {
			t.Error(err)
		}
		fixture.mu.Lock()
		fixture.seen = append(fixture.seen, exchangeSeen{request.Method, request.URL.Path, request.URL.RawQuery, request.Header.Clone(), body.Bytes()})
		fixture.mu.Unlock()
		reply(writer, request)
	}))
	t.Cleanup(fixture.server.Close)
	fixture.record, fixture.disabled = recordFixture(t, "a"), recordFixture(t, "c")
	other := recordFixture(t, "b")
	fixture.disabled.Disabled = true
	fixture.service = newService(accountHost(t, []storageRecord{fixture.record, other, fixture.disabled}))
	seedSessions(t, fixture.service, map[string]sessionToken{
		other.TokenRef:            {encodedToken("SID=other")},
		fixture.disabled.TokenRef: {encodedToken("SID=disabled")},
	})
	auth, err := authFromRecord(fixture.record)
	if err != nil {
		t.Fatal(err)
	}
	identity := credentialInspection{AccountSHA256: testAccountDigest, AuthUser: uint64(authUser)}
	if err := fixture.service.sessions.write(localSession{Target: fixture.record, Projection: string(auth.StorageJSON), Token: encodedTokenUser("SID=one; SAPISID=secret", authUser), Identity: identity, State: localReady}); err != nil {
		t.Fatal(err)
	}
	fixture.service.client = fixture.server.Client()
	fixture.service.sessionExchangeOriginOverride = fixture.server.URL
	fixture.service.config.SessionExchangeAccounts = []string{fixture.record.ID, fixture.disabled.ID, "gemini-web-d.json"}
	return fixture
}

func (fixture *exchangeFixture) exchange(t *testing.T, request sessionExchangeRequest) (sessionExchangeResult, error) {
	t.Helper()
	result, err := fixture.service.sessionExchange(t.Context(), managementRequest{Method: "POST", Path: sessionExchangePath, HostCallbackID: "scope-list", Body: jsonFixture(t, request)})
	if err != nil {
		return sessionExchangeResult{}, err
	}
	typed, ok := result.(sessionExchangeResult)
	if !ok {
		t.Fatalf("result type = %T", result)
	}
	return typed, nil
}

func exchangeOK(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/plain")
	if _, err := writer.Write([]byte("ok")); err != nil {
		return
	}
}

func exchangeCode(t *testing.T, err error, status int) string {
	t.Helper()
	var public *publicError
	if err == nil {
		t.Fatal("exchange succeeded, want a refusal")
	}
	if !errors.As(err, &public) || public.HTTPStatus != status {
		t.Fatalf("error = %#v, want HTTP %d", err, status)
	}
	return public.Code
}

func TestSessionExchangeBrokersFlowAsTheCredentialAccount(t *testing.T) {
	fixture := newExchangeFixture(t, 2, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Location", "/next")
		writer.Header().Set("X-Internal", "hidden")
		writer.Header().Add("Set-Cookie", "elsewhere=1; Domain=example.org; Path=/")
		writer.WriteHeader(http.StatusCreated)
		if _, err := writer.Write([]byte(`{"done":true}`)); err != nil {
			return
		}
	})
	result, err := fixture.exchange(t, sessionExchangeRequest{
		AuthID: fixture.record.ID, Method: "POST",
		URL: "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?rpcids=x&source-path=%2Fprojects&sig=a%2Bb",
		Headers: http.Header{
			"cookie": {"stolen=1"}, "Authorization": {"Bearer caller"}, "x-goog-authuser": {"9"},
			"Referer": {"https://flow.google.com/projects"}, "Origin": {"https://flow.google.com"},
			"Content-Type": {"application/x-www-form-urlencoded"},
		},
		Body: []byte("f.req=1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != http.StatusCreated || string(result.Body) != `{"done":true}` {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Headers) != 2 || result.Headers.Get("Content-Type") != "application/json" || result.Headers.Get("Location") != "/next" {
		t.Fatalf("headers = %v, want only Content-Type and Location", result.Headers)
	}
	seen := fixture.requests()
	if len(seen) != 1 {
		t.Fatalf("upstream requests = %d", len(seen))
	}
	got := seen[0]
	query, err := url.ParseQuery(got.rawQuery)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case got.method != "POST" || string(got.body) != "f.req=1":
		t.Fatalf("request = %s %q", got.method, got.body)
	case got.path != "/u/2/_/AiSandboxAngularFrontend/data/batchexecute":
		t.Fatalf("path = %q, want the credential's /u/2 prefix", got.path)
	case query.Get("source-path") != "/u/2/projects" || !strings.Contains(got.rawQuery, "sig=a%2Bb") || query.Get("rpcids") != "x":
		t.Fatalf("query = %q", got.rawQuery)
	case got.header.Get("Cookie") != "SID=one; SAPISID=secret" || got.header.Get("X-Goog-AuthUser") != "2":
		t.Fatalf("cookie/auth user = %q/%q", got.header.Get("Cookie"), got.header.Get("X-Goog-AuthUser"))
	case got.header.Get("Authorization") != "" || len(got.header.Values("Cookie")) != 1 || len(got.header.Values("X-Goog-AuthUser")) != 1:
		t.Fatalf("caller credentials survived: %v", got.header)
	case got.header.Get("Referer") != "https://flow.google.com/u/2/projects" || got.header.Get("Origin") != "https://flow.google.com":
		t.Fatalf("referer/origin = %q/%q", got.header.Get("Referer"), got.header.Get("Origin"))
	case got.header.Get("User-Agent") != sessionExchangeUserAgent || got.header.Get("Content-Type") != "application/x-www-form-urlencoded":
		t.Fatalf("user agent/content type = %q/%q", got.header.Get("User-Agent"), got.header.Get("Content-Type"))
	}
}

func TestSessionExchangeLeavesFlowPathUnprefixedForTheDefaultAccount(t *testing.T) {
	fixture := newExchangeFixture(t, 0, exchangeOK)
	if _, err := fixture.exchange(t, sessionExchangeRequest{AuthID: fixture.record.ID, Method: "GET", URL: "https://flow.google.com/projects?source-path=%2Fprojects"}); err != nil {
		t.Fatal(err)
	}
	got := fixture.requests()[0]
	if got.path != "/projects" || got.rawQuery != "source-path=%2Fprojects" || got.header.Get("X-Goog-AuthUser") != "0" {
		t.Fatalf("request = %s ?%s authuser=%q", got.path, got.rawQuery, got.header.Get("X-Goog-AuthUser"))
	}
}

func TestSessionExchangeCaptchaHostKeepsPathQueryAndCallerAgent(t *testing.T) {
	fixture := newExchangeFixture(t, 2, exchangeOK)
	rawQuery := "ar=1&k=site-key&co=aHR0cHM6Ly9mbG93Lmdvb2dsZS5jb206NDQz&source-path=%2Fprojects"
	if _, err := fixture.exchange(t, sessionExchangeRequest{
		AuthID: fixture.record.ID, Method: "GET", URL: "https://www.google.com/recaptcha/enterprise/anchor?" + rawQuery,
		Headers: http.Header{"User-Agent": {"Solver/1.0"}, "Referer": {"https://flow.google.com/projects"}},
	}); err != nil {
		t.Fatal(err)
	}
	got := fixture.requests()[0]
	if got.path != "/recaptcha/enterprise/anchor" || got.rawQuery != rawQuery {
		t.Fatalf("request = %s ?%s, want the path and signed query untouched", got.path, got.rawQuery)
	}
	if got.header.Get("User-Agent") != "Solver/1.0" || got.header.Get("Cookie") != "SID=one; SAPISID=secret" || got.header.Get("X-Goog-AuthUser") != "2" {
		t.Fatalf("headers = %v", got.header)
	}
	if got.header.Get("Referer") != "https://flow.google.com/u/2/projects" {
		t.Fatalf("referer = %q", got.header.Get("Referer"))
	}
}

func TestSessionExchangeRefusesWhatIsNotAllowlisted(t *testing.T) {
	cases := []struct {
		name    string
		request sessionExchangeRequest
		status  int
		code    string
	}{
		{"plain http", sessionExchangeRequest{AuthID: "gemini-web-a.json", Method: "GET", URL: "http://flow.google.com/projects"}, 403, "session_exchange_url_denied"},
		{"other port", sessionExchangeRequest{AuthID: "gemini-web-a.json", Method: "GET", URL: "https://flow.google.com:8443/projects"}, 403, "session_exchange_url_denied"},
		{"userinfo", sessionExchangeRequest{AuthID: "gemini-web-a.json", Method: "GET", URL: "https://user:pass@flow.google.com/projects"}, 403, "session_exchange_url_denied"},
		{"foreign host", sessionExchangeRequest{AuthID: "gemini-web-a.json", Method: "GET", URL: "https://evil.example/projects"}, 403, "session_exchange_url_denied"},
		{"lookalike host", sessionExchangeRequest{AuthID: "gemini-web-a.json", Method: "GET", URL: "https://flow.google.com.evil.example/projects"}, 403, "session_exchange_url_denied"},
		{"other google host", sessionExchangeRequest{AuthID: "gemini-web-a.json", Method: "GET", URL: "https://accounts.google.com/signin"}, 403, "session_exchange_url_denied"},
		{"mixed case host", sessionExchangeRequest{AuthID: "gemini-web-a.json", Method: "GET", URL: "https://FLOW.google.com/projects"}, 403, "session_exchange_url_denied"},
		{"google search", sessionExchangeRequest{AuthID: "gemini-web-a.json", Method: "GET", URL: "https://www.google.com/search?q=x"}, 403, "session_exchange_url_denied"},
		{"bare recaptcha", sessionExchangeRequest{AuthID: "gemini-web-a.json", Method: "GET", URL: "https://www.google.com/recaptcha"}, 403, "session_exchange_url_denied"},
		{"dot segments", sessionExchangeRequest{AuthID: "gemini-web-a.json", Method: "GET", URL: "https://www.google.com/recaptcha/../search"}, 403, "session_exchange_url_denied"},
		{"backslash", sessionExchangeRequest{AuthID: "gemini-web-a.json", Method: "GET", URL: "https://flow.google.com\\@evil.example/"}, 400, "session_exchange_url_invalid"},
		{"caller prefix", sessionExchangeRequest{AuthID: "gemini-web-a.json", Method: "GET", URL: "https://flow.google.com/u/3/projects"}, 403, "session_exchange_path_denied"},
		{"caller prefix escaped", sessionExchangeRequest{AuthID: "gemini-web-a.json", Method: "GET", URL: "https://flow.google.com/%75/3/projects"}, 403, "session_exchange_path_denied"},
		{"method", sessionExchangeRequest{AuthID: "gemini-web-a.json", Method: "DELETE", URL: "https://flow.google.com/projects"}, 400, "session_exchange_method_invalid"},
		{"unlisted account", sessionExchangeRequest{AuthID: "gemini-web-b.json", Method: "GET", URL: "https://flow.google.com/projects"}, 403, "session_exchange_account_denied"},
		{"disabled account", sessionExchangeRequest{AuthID: "gemini-web-c.json", Method: "GET", URL: "https://flow.google.com/projects"}, 409, "session_exchange_account_disabled"},
		{"missing account", sessionExchangeRequest{AuthID: "gemini-web-d.json", Method: "GET", URL: "https://flow.google.com/projects"}, 404, "session_exchange_account_not_found"},
	}
	fixture := newExchangeFixture(t, 2, exchangeOK)
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := fixture.exchange(t, test.request)
			if code := exchangeCode(t, err, test.status); code != test.code {
				t.Fatalf("code = %q, want %q", code, test.code)
			}
		})
	}
	if seen := fixture.requests(); len(seen) != 0 {
		t.Fatalf("refused requests reached upstream: %+v", seen)
	}
	if _, err := fixture.service.sessionExchange(t.Context(), managementRequest{HostCallbackID: "scope-list", Body: []byte("{")}); exchangeCode(t, err, 400) != "session_exchange_request_invalid" {
		t.Fatal("malformed body was not refused as invalid")
	}
}

func TestSessionExchangePersistsRotationWithoutLeakingCookies(t *testing.T) {
	fixture := newExchangeFixture(t, 2, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Add("Set-Cookie", "SID=rotated; Domain=google.com; Path=/; Secure; HttpOnly")
		writer.Header().Set("Content-Type", "text/plain")
		if _, err := writer.Write([]byte("ok")); err != nil {
			return
		}
	})
	request := sessionExchangeRequest{AuthID: fixture.record.ID, Method: "GET", URL: "https://flow.google.com/projects"}
	result, err := fixture.exchange(t, request)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(jsonFixture(t, result))
	for _, leaked := range []string{"rotated", "secret", "Set-Cookie", "SID="} {
		if strings.Contains(encoded, leaked) {
			t.Fatalf("result leaks %q: %s", leaked, encoded)
		}
	}
	token, err := fixture.service.resolveLocal(fixture.record)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := decodeWebCredential(token)
	if err != nil {
		t.Fatal(err)
	}
	if credential.Cookie != "SID=rotated; SAPISID=secret" || credential.AuthUser != 2 {
		t.Fatalf("stored credential = %+v, want the rotation in the sole store", credential)
	}
	if _, err := fixture.exchange(t, request); err != nil {
		t.Fatal(err)
	}
	if cookie := fixture.requests()[1].header.Get("Cookie"); cookie != "SID=rotated; SAPISID=secret" {
		t.Fatalf("second exchange sent %q, want the rotated jar", cookie)
	}
}

func TestSessionExchangeReportsBusyWithoutSending(t *testing.T) {
	fixture := newExchangeFixture(t, 2, exchangeOK)
	lease, err := fixture.service.acquireCredential(fixture.record.TokenRef, true)
	if err != nil {
		t.Fatal(err)
	}
	request := sessionExchangeRequest{AuthID: fixture.record.ID, Method: "GET", URL: "https://flow.google.com/projects"}
	_, err = fixture.exchange(t, request)
	if code := exchangeCode(t, err, 409); code != "session_exchange_busy" {
		t.Fatalf("code = %q", code)
	}
	if seen := fixture.requests(); len(seen) != 0 {
		t.Fatalf("busy exchange reached upstream: %+v", seen)
	}
	lease.guard.Unlock()
	if _, err := fixture.exchange(t, request); err != nil {
		t.Fatalf("retry after release: %v", err)
	}
}

func TestSessionExchangeBoundsTheResponse(t *testing.T) {
	fixture := newExchangeFixture(t, 2, func(writer http.ResponseWriter, request *http.Request) {
		size, err := strconv.Atoi(request.URL.Query().Get("size"))
		if err != nil {
			t.Error(err)
			return
		}
		chunk := bytes.Repeat([]byte("x"), 1<<20)
		for written := 0; written < size; written += len(chunk) {
			if _, err := writer.Write(chunk[:min(len(chunk), size-written)]); err != nil {
				return
			}
		}
	})
	request := sessionExchangeRequest{AuthID: fixture.record.ID, Method: "GET", URL: "https://flow.google.com/blob?size=" + strconv.Itoa(sessionExchangeLimit)}
	result, err := fixture.exchange(t, request)
	if err != nil || len(result.Body) != sessionExchangeLimit {
		t.Fatalf("limit-sized response = %d bytes, %v", len(result.Body), err)
	}
	request.URL = "https://flow.google.com/blob?size=" + strconv.Itoa(sessionExchangeLimit+1)
	_, err = fixture.exchange(t, request)
	if code := exchangeCode(t, err, 502); code != "session_exchange_response_too_large" {
		t.Fatalf("code = %q", code)
	}
}

func TestSessionExchangeReturnsRedirectsUnfollowed(t *testing.T) {
	fixture := newExchangeFixture(t, 2, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "https://accounts.google.com/signin")
		writer.WriteHeader(http.StatusFound)
	})
	result, err := fixture.exchange(t, sessionExchangeRequest{AuthID: fixture.record.ID, Method: "GET", URL: "https://flow.google.com/projects"})
	if err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != http.StatusFound || result.Headers.Get("Location") != "https://accounts.google.com/signin" {
		t.Fatalf("result = %+v", result)
	}
	if seen := fixture.requests(); len(seen) != 1 {
		t.Fatalf("upstream requests = %d, want the redirect left to the caller", len(seen))
	}
}

func TestSessionExchangeNamesTransportFailureWithoutDetail(t *testing.T) {
	fixture := newExchangeFixture(t, 2, exchangeOK)
	fixture.service.sessionExchangeOriginOverride = "https://127.0.0.1:1"
	_, err := fixture.exchange(t, sessionExchangeRequest{AuthID: fixture.record.ID, Method: "GET", URL: "https://flow.google.com/projects"})
	if code := exchangeCode(t, err, 502); code != "session_exchange_transport_failed" {
		t.Fatalf("code = %q", code)
	}
	if strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error leaks detail: %v", err)
	}
	if encoded, marshalErr := json.Marshal(err); marshalErr != nil || strings.Contains(string(encoded), "SID") {
		t.Fatalf("encoded error = %s, %v", encoded, marshalErr)
	}
}
