package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testBotToken      = "test-bot-token"
	testManagementKey = "test-management-key"
	testChatID        = int64(-100123)

	geminiPrimaryKey = "AIzaSyEXAMPLEKEY1234"
	geminiSecondKey  = "AIzaSySECONDKEYabcd"
	codexKey         = "sk-codexkeyWXYZ"
)

// apiKeyUsageFixture mirrors a real GET /v0/management/api-key-usage payload.
const apiKeyUsageFixture = `{
  "gemini": {
    "https://generativelanguage.googleapis.com|` + geminiPrimaryKey + `": {
      "success": 100,
      "failed": 2,
      "recent_requests": [{"time":"09:00-09:03","success":5,"failed":1}]
    },
    "|` + geminiSecondKey + `": {
      "success": 10,
      "failed": 0,
      "recent_requests": [{"time":"09:00-09:03","success":2,"failed":0}]
    }
  },
  "codex": {
    "https://api.openai.com|` + codexKey + `": {
      "success": 7,
      "failed": 3,
      "recent_requests": [{"time":"09:00-09:03","success":1,"failed":2}]
    }
  }
}`

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireEqual[T comparable](t *testing.T, want, got T) {
	t.Helper()
	if got != want {
		t.Fatalf("want %v, got %v", want, got)
	}
}

func requireContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected %q to contain %q", haystack, needle)
	}
}

func requireAbsent(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("expected %q to be absent from %q", needle, haystack)
	}
}

// telegramCall records one Bot API invocation observed by the stub.
type telegramCall struct {
	method string
	form   url.Values
}

// telegramStub is an httptest Telegram Bot API that records every call.
type telegramStub struct {
	server *httptest.Server

	mu    sync.Mutex
	calls []telegramCall
}

func newTelegramStub(t *testing.T) *telegramStub {
	t.Helper()
	stub := &telegramStub{}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if errParse := r.ParseForm(); errParse != nil {
			http.Error(w, errParse.Error(), http.StatusBadRequest)
			return
		}
		method := path.Base(r.URL.Path)
		stub.record(method, r.Form)
		w.Header().Set("Content-Type", "application/json")
		body := `{"ok":true,"result":true}`
		if method == "getUpdates" {
			body = `{"ok":true,"result":[]}`
		}
		if _, errWrite := w.Write([]byte(body)); errWrite != nil {
			t.Errorf("write telegram stub response: %v", errWrite)
		}
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *telegramStub) record(method string, form url.Values) {
	copied := make(url.Values, len(form))
	for key, values := range form {
		copied[key] = append([]string(nil), values...)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, telegramCall{method: method, form: copied})
}

func (s *telegramStub) callsFor(method string) []telegramCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	matched := make([]telegramCall, 0, len(s.calls))
	for _, call := range s.calls {
		if call.method == method {
			matched = append(matched, call)
		}
	}
	return matched
}

func (s *telegramStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// cpaStub is an httptest CLIProxyAPI management API serving the usage fixture.
type cpaStub struct {
	server *httptest.Server

	mu          sync.Mutex
	authHeaders []string
}

func newCPAStub(t *testing.T) *cpaStub {
	t.Helper()
	stub := &cpaStub{}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.mu.Lock()
		stub.authHeaders = append(stub.authHeaders, r.Header.Get("Authorization"))
		stub.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		body := ""
		switch r.URL.Path {
		case cpaAPIKeyUsagePath:
			body = apiKeyUsageFixture
		case cpaUsageStatusPath:
			body = `{"usage-statistics-enabled":true}`
		default:
			http.NotFound(w, r)
			return
		}
		if _, errWrite := w.Write([]byte(body)); errWrite != nil {
			t.Errorf("write CPA stub response: %v", errWrite)
		}
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *cpaStub) lastAuthHeader() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.authHeaders) == 0 {
		return ""
	}
	return s.authHeaders[len(s.authHeaders)-1]
}

func newTestBotConfig(t *testing.T, telegram *telegramStub, cpa *cpaStub, allowed []int64) botConfig {
	t.Helper()
	return botConfig{
		telegram: telegramConfig{
			baseURL:     telegram.server.URL,
			token:       testBotToken,
			pollTimeout: time.Second,
			callTimeout: 5 * time.Second,
		},
		cpaBaseURL:     cpa.server.URL,
		managementKey:  testManagementKey,
		allowedChatIDs: allowed,
		reportAt:       timeOfDay{hour: 9},
		reportEnabled:  true,
		statePath:      filepath.Join(t.TempDir(), "telegram-usage-bot.json"),
	}
}

func newTestBot(t *testing.T, telegram *telegramStub, cpa *cpaStub, allowed []int64) *bot {
	t.Helper()
	config := newTestBotConfig(t, telegram, cpa, allowed)
	client := &http.Client{}
	service := newBot(
		config,
		newTelegramClient(client, config.telegram),
		newCPAClient(client, config.cpaBaseURL, config.managementKey, 5*time.Second),
	)
	service.now = func() time.Time { return time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC) }
	return service
}
