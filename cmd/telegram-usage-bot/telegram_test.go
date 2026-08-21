package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestTelegramClient(baseURL string) *telegramClient {
	return newTelegramClient(&http.Client{}, telegramConfig{
		baseURL:     baseURL,
		token:       testBotToken,
		pollTimeout: time.Second,
		callTimeout: 5 * time.Second,
	})
}

func TestTelegramClient_requestsMessageAndCallbackUpdates_whenLongPolling(t *testing.T) {
	// Given
	stub := newTelegramStub(t)
	client := newTestTelegramClient(stub.server.URL)

	// When
	updates, errUpdates := client.GetUpdates(context.Background(), 77)

	// Then
	requireNoError(t, errUpdates)
	requireEqual(t, 0, len(updates))
	calls := stub.callsFor("getUpdates")
	requireEqual(t, 1, len(calls))
	requireEqual(t, "77", calls[0].form.Get("offset"))
	requireEqual(t, "1", calls[0].form.Get("timeout"))
	requireEqual(t, allowedUpdatesJSON, calls[0].form.Get("allowed_updates"))
}

func TestTelegramClient_decodesUpdates_whenTelegramReturnsResults(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, errWrite := w.Write([]byte(`{"ok":true,"result":[{"update_id":9,"callback_query":{"id":"cb","data":"usage:summary","message":{"message_id":3,"chat":{"id":-100123}}}}]}`))
		if errWrite != nil {
			t.Errorf("write telegram response: %v", errWrite)
		}
	}))
	defer server.Close()
	client := newTestTelegramClient(server.URL)

	// When
	updates, errUpdates := client.GetUpdates(context.Background(), 0)

	// Then
	requireNoError(t, errUpdates)
	requireEqual(t, 1, len(updates))
	requireEqual(t, int64(9), updates[0].UpdateID)
	requireEqual(t, actionSummary, updates[0].CallbackQuery.Data)
	requireEqual(t, testChatID, updates[0].CallbackQuery.Message.Chat.ID)
}

func TestTelegramClient_sendsHTMLMessageWithKeyboard(t *testing.T) {
	// Given
	stub := newTelegramStub(t)
	client := newTestTelegramClient(stub.server.URL)

	// When
	errSend := client.SendMessage(context.Background(), testChatID, "<b>hi</b>", usageKeyboard())

	// Then
	requireNoError(t, errSend)
	calls := stub.callsFor("sendMessage")
	requireEqual(t, 1, len(calls))
	requireEqual(t, "-100123", calls[0].form.Get("chat_id"))
	requireEqual(t, "HTML", calls[0].form.Get("parse_mode"))
	requireEqual(t, "<b>hi</b>", calls[0].form.Get("text"))
	requireContains(t, calls[0].form.Get("reply_markup"), actionFailures)
}

func TestTelegramClient_editsMessageText(t *testing.T) {
	// Given
	stub := newTelegramStub(t)
	client := newTestTelegramClient(stub.server.URL)

	// When
	errEdit := client.EditMessageText(context.Background(), testChatID, 42, "refreshed", nil)

	// Then
	requireNoError(t, errEdit)
	calls := stub.callsFor("editMessageText")
	requireEqual(t, 1, len(calls))
	requireEqual(t, "42", calls[0].form.Get("message_id"))
	requireEqual(t, "refreshed", calls[0].form.Get("text"))
	requireEqual(t, "", calls[0].form.Get("reply_markup"))
}

func TestTelegramClient_returnsTypedError_whenTelegramRejectsCall(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		if _, errWrite := w.Write([]byte(`{"ok":false,"description":"message is not modified"}`)); errWrite != nil {
			t.Errorf("write telegram response: %v", errWrite)
		}
	}))
	defer server.Close()
	client := newTestTelegramClient(server.URL)

	// When
	errEdit := client.EditMessageText(context.Background(), testChatID, 42, "same", nil)

	// Then
	var apiErr *telegramAPIError
	if !errors.As(errEdit, &apiErr) {
		t.Fatalf("expected *telegramAPIError, got %v", errEdit)
	}
	requireEqual(t, http.StatusBadRequest, apiErr.Status)
	requireEqual(t, "editMessageText", apiErr.Method)
	requireContains(t, apiErr.Error(), "message is not modified")
}

func TestTelegramClient_redactsBotToken_whenTransportFails(t *testing.T) {
	// Given
	client := newTelegramClient(&http.Client{Transport: failingTransport{}}, telegramConfig{
		baseURL:     "https://api.telegram.org",
		token:       testBotToken,
		pollTimeout: time.Second,
		callTimeout: time.Second,
	})

	// When
	errAnswer := client.AnswerCallbackQuery(context.Background(), "cb-1")

	// Then
	if errAnswer == nil {
		t.Fatal("expected transport error")
	}
	if strings.Contains(errAnswer.Error(), testBotToken) {
		t.Fatalf("transport error leaked bot token: %v", errAnswer)
	}
	requireContains(t, errAnswer.Error(), redactedValue)
}

// failingTransport fails with an error containing the request URL, which embeds
// the bot token, so the redaction boundary can be observed.
type failingTransport struct{}

func (failingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return nil, errors.New("dial failed for " + request.URL.String())
}
