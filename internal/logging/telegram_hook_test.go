package logging

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	log "github.com/sirupsen/logrus"
)

func TestTelegramHook_sends_sanitized_error_to_configured_chat(t *testing.T) {
	type requestPayload struct {
		ChatID    string `json:"chat_id"`
		Text      string `json:"text"`
		ParseMode string `json:"parse_mode"`
	}

	// Given
	requests := make(chan requestPayload, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/bottest-bot-token/sendMessage" {
			t.Errorf("request path = %q, want Telegram sendMessage path", got)
		}
		var payload requestPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- payload
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	hook := newTelegramHook(server.Client(), server.URL)
	hook.Configure(config.TelegramConfig{
		Enabled:  true,
		BotToken: "test-bot-token",
		ChatID:   "-1001234567890",
	})
	defer hook.Close()
	entry := &log.Entry{
		Logger:  log.New(),
		Level:   log.ErrorLevel,
		Message: "upstream failed authorization=Bearer super-secret-value",
		Time:    time.Date(2026, time.August, 10, 12, 34, 56, 0, time.UTC),
		Data: log.Fields{
			"error":      "request rejected for token=another-secret-value",
			"model":      "gpt-5.6",
			"provider":   "openai",
			"request_id": "req-123",
			"status":     "400",
		},
		Caller: &runtime.Frame{File: "/tmp/executor.go", Line: 42},
	}

	// When
	if err := hook.Fire(entry); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}

	// Then
	select {
	case payload := <-requests:
		if payload.ChatID != "-1001234567890" {
			t.Fatalf("chat_id = %q, want configured chat", payload.ChatID)
		}
		if payload.ParseMode != "HTML" {
			t.Fatalf("parse_mode = %q, want HTML", payload.ParseMode)
		}
		for _, want := range []string{
			"<b>CLIProxyAPI · Model request failed</b>",
			"<code>openai · gpt-5.6 · HTTP 400</code>",
			"<b>Error</b>",
			"<pre>request rejected for token=&lt;redacted&gt;</pre>",
			"<b>Message</b>",
			"<pre>upstream failed authorization=&lt;redacted&gt;</pre>",
			"<b>Request</b>  <code>req-123</code>",
			"<b>Source</b>   <code>executor.go:42</code>",
		} {
			if !strings.Contains(payload.Text, want) {
				t.Errorf("text = %q, want substring %q", payload.Text, want)
			}
		}
		for _, secret := range []string{"super-secret-value", "another-secret-value", "test-bot-token"} {
			if strings.Contains(payload.Text, secret) {
				t.Errorf("text contains secret %q", secret)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Telegram notification")
	}
}

func TestTelegramHook_subscribes_only_to_error_levels(t *testing.T) {
	// Given
	hook := newTelegramHook(http.DefaultClient, "https://api.telegram.org")
	defer hook.Close()

	// When
	levels := hook.Levels()

	// Then
	want := []log.Level{log.ErrorLevel}
	if len(levels) != len(want) {
		t.Fatalf("Levels() = %v, want %v", levels, want)
	}
	for i := range want {
		if levels[i] != want[i] {
			t.Fatalf("Levels()[%d] = %v, want %v", i, levels[i], want[i])
		}
	}
}

func TestTelegramHook_preserves_in_flight_delivery_when_config_is_unchanged(t *testing.T) {
	// Given
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	completed := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-release:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
			completed <- struct{}{}
		case <-r.Context().Done():
			canceled <- struct{}{}
		}
	}))
	defer server.Close()

	hook := newTelegramHook(server.Client(), server.URL)
	cfg := config.TelegramConfig{Enabled: true, BotToken: "test-bot-token", ChatID: "chat"}
	hook.Configure(cfg)
	defer hook.Close()
	entry := &log.Entry{Logger: log.New(), Level: log.ErrorLevel, Message: "error", Time: time.Now()}
	if err := hook.Fire(entry); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for request to start")
	}

	// When
	hook.Configure(cfg)
	close(release)

	// Then
	select {
	case <-completed:
	case <-canceled:
		t.Fatal("unchanged configuration canceled in-flight delivery")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for request to complete")
	}
}

func TestTelegramWorker_rejects_unsuccessful_response_envelope(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400}`))
	}))
	defer server.Close()
	worker := &telegramWorker{
		client:   server.Client(),
		baseURL:  server.URL,
		botToken: "test-bot-token",
		chatID:   "chat",
	}

	// When
	err := worker.send(t.Context(), telegramAlert{text: "error"})

	// Then
	if err == nil || !strings.Contains(err.Error(), "code 400") {
		t.Fatalf("send() error = %v, want Telegram error code", err)
	}
}
