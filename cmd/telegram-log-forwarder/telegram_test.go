package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramSender_sendsHTMLMessage_toConfiguredChat(t *testing.T) {
	// Given
	received := make(chan struct {
		chatID    string
		parseMode string
		text      string
		err       error
	}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errParse := r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		_, errWrite := w.Write([]byte(`{"ok":true}`))
		received <- struct {
			chatID    string
			parseMode string
			text      string
			err       error
		}{
			chatID:    r.Form.Get("chat_id"),
			parseMode: r.Form.Get("parse_mode"),
			text:      r.Form.Get("text"),
			err:       errors.Join(errParse, errWrite),
		}
	}))
	defer server.Close()
	sender := newTelegramSender(server.Client(), telegramConfig{
		baseURL: server.URL,
		token:   "test-token",
		chatID:  "-100123",
	})

	// When
	err := sender.Send(context.Background(), errorAlert{Status: 500, Message: "failed"})

	// Then
	requireNoError(t, err)
	request := <-received
	requireNoError(t, request.err)
	requireEqual(t, "-100123", request.chatID)
	requireEqual(t, "HTML", request.parseMode)
	if request.text == "" {
		t.Fatal("telegram message is empty")
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return nil, errors.New("dial failed for " + request.URL.String())
}

func TestTelegramSender_redactsBotToken_whenTransportFails(t *testing.T) {
	// Given
	const token = "secret-bot-token"
	sender := newTelegramSender(&http.Client{Transport: failingTransport{}}, telegramConfig{
		baseURL: "https://api.telegram.org",
		token:   token,
		chatID:  "-100123",
	})

	// When
	err := sender.Send(context.Background(), errorAlert{Status: 500, Message: "failed"})

	// Then
	if err == nil {
		t.Fatal("expected transport error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("transport error leaked bot token: %v", err)
	}
}

func TestTelegramSender_redactsBotToken_whenBaseURLIsMalformed(t *testing.T) {
	// Given
	const token = "secret-bot-token"
	sender := newTelegramSender(http.DefaultClient, telegramConfig{
		baseURL: "https://[broken",
		token:   token,
		chatID:  "-100123",
	})

	// When
	err := sender.Send(context.Background(), errorAlert{Status: 500, Message: "failed"})

	// Then
	if err == nil {
		t.Fatal("expected request construction error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("request construction error leaked bot token: %v", err)
	}
}
