package main

import (
	"context"
	"testing"
	"time"
)

func TestBot_sendsSummaryWithInlineKeyboard_whenUsageCommandReceived(t *testing.T) {
	// Given
	telegram := newTelegramStub(t)
	cpa := newCPAStub(t)
	service := newTestBot(t, telegram, cpa, []int64{testChatID})

	// When
	service.handleUpdate(context.Background(), telegramUpdate{
		UpdateID: 11,
		Message:  &telegramMessage{MessageID: 5, Chat: telegramChat{ID: testChatID}, Text: "/usage"},
	})

	// Then
	sends := telegram.callsFor("sendMessage")
	requireEqual(t, 1, len(sends))
	form := sends[0].form
	requireEqual(t, "-100123", form.Get("chat_id"))
	requireEqual(t, "HTML", form.Get("parse_mode"))
	requireContains(t, form.Get("text"), "CPA Usage Summary")
	requireContains(t, form.Get("text"), "Success: 117")
	requireContains(t, form.Get("reply_markup"), actionProviders)
	requireContains(t, form.Get("reply_markup"), actionPing)
	requireEqual(t, "Bearer "+testManagementKey, cpa.lastAuthHeader())
}

func TestBot_prependsHelp_whenHelpCommandReceived(t *testing.T) {
	// Given
	telegram := newTelegramStub(t)
	cpa := newCPAStub(t)
	service := newTestBot(t, telegram, cpa, []int64{testChatID})

	// When
	service.handleUpdate(context.Background(), telegramUpdate{
		UpdateID: 12,
		Message:  &telegramMessage{MessageID: 6, Chat: telegramChat{ID: testChatID}, Text: "/help@usage_bot"},
	})

	// Then
	sends := telegram.callsFor("sendMessage")
	requireEqual(t, 1, len(sends))
	requireContains(t, sends[0].form.Get("text"), "CPA Usage Bot")
	requireContains(t, sends[0].form.Get("text"), "CPA Usage Summary")
}

func TestBot_answersAndEditsWithProviderBreakdown_whenProvidersCallbackReceived(t *testing.T) {
	// Given
	telegram := newTelegramStub(t)
	cpa := newCPAStub(t)
	service := newTestBot(t, telegram, cpa, []int64{testChatID})

	// When
	service.handleUpdate(context.Background(), telegramUpdate{
		UpdateID: 21,
		CallbackQuery: &telegramCallbackQuery{
			ID:      "cb-1",
			Data:    actionProviders,
			Message: &telegramMessage{MessageID: 42, Chat: telegramChat{ID: testChatID}},
		},
	})

	// Then
	answers := telegram.callsFor("answerCallbackQuery")
	requireEqual(t, 1, len(answers))
	requireEqual(t, "cb-1", answers[0].form.Get("callback_query_id"))

	edits := telegram.callsFor("editMessageText")
	requireEqual(t, 1, len(edits))
	requireEqual(t, "42", edits[0].form.Get("message_id"))
	text := edits[0].form.Get("text")
	requireContains(t, text, "Usage by Provider")
	requireContains(t, text, "gemini")
	requireContains(t, text, "codex")
	requireContains(t, text, maskAPIKey(geminiPrimaryKey))
	requireAbsent(t, text, geminiPrimaryKey)
	requireEqual(t, 0, len(telegram.callsFor("sendMessage")))
}

func TestBot_sendsNewMessage_whenCallbackHasNoOriginalMessage(t *testing.T) {
	// Given
	telegram := newTelegramStub(t)
	cpa := newCPAStub(t)
	service := newTestBot(t, telegram, cpa, []int64{testChatID})
	service.allowed[0] = struct{}{}

	// When
	service.handleUpdate(context.Background(), telegramUpdate{
		UpdateID:      22,
		CallbackQuery: &telegramCallbackQuery{ID: "cb-2", Data: actionPing},
	})

	// Then
	requireEqual(t, 1, len(telegram.callsFor("answerCallbackQuery")))
	sends := telegram.callsFor("sendMessage")
	requireEqual(t, 1, len(sends))
	requireContains(t, sends[0].form.Get("text"), "CPA Status")
	requireEqual(t, 0, len(telegram.callsFor("editMessageText")))
}

func TestBot_ignoresMessage_whenChatIsNotWhitelisted(t *testing.T) {
	// Given
	telegram := newTelegramStub(t)
	cpa := newCPAStub(t)
	service := newTestBot(t, telegram, cpa, []int64{testChatID})

	// When
	service.handleUpdate(context.Background(), telegramUpdate{
		UpdateID: 31,
		Message:  &telegramMessage{MessageID: 7, Chat: telegramChat{ID: 999}, Text: "/usage"},
	})

	// Then
	requireEqual(t, 0, telegram.callCount())
}

func TestBot_ignoresCallback_whenChatIsNotWhitelisted(t *testing.T) {
	// Given
	telegram := newTelegramStub(t)
	cpa := newCPAStub(t)
	service := newTestBot(t, telegram, cpa, []int64{testChatID})

	// When
	service.handleUpdate(context.Background(), telegramUpdate{
		UpdateID: 32,
		CallbackQuery: &telegramCallbackQuery{
			ID:      "cb-3",
			Data:    actionSummary,
			Message: &telegramMessage{MessageID: 8, Chat: telegramChat{ID: 999}},
		},
	})

	// Then
	requireEqual(t, 0, telegram.callCount())
}

func TestBot_reportsCPAUnavailable_whenManagementAPIIsDown(t *testing.T) {
	// Given
	telegram := newTelegramStub(t)
	cpa := newCPAStub(t)
	service := newTestBot(t, telegram, cpa, []int64{testChatID})
	cpa.server.Close()

	// When
	service.handleUpdate(context.Background(), telegramUpdate{
		UpdateID: 41,
		Message:  &telegramMessage{MessageID: 9, Chat: telegramChat{ID: testChatID}, Text: "/usage"},
	})

	// Then
	sends := telegram.callsFor("sendMessage")
	requireEqual(t, 1, len(sends))
	requireContains(t, sends[0].form.Get("text"), "CPA unavailable")
	requireAbsent(t, sends[0].form.Get("text"), testManagementKey)
}

func TestBot_sendsDailyReportOnce_whenScheduledTimeIsReached(t *testing.T) {
	// Given
	telegram := newTelegramStub(t)
	cpa := newCPAStub(t)
	service := newTestBot(t, telegram, cpa, []int64{testChatID})
	tick := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		service.reportLoop(ctx, tick)
	}()

	// When
	// Each send returns only once the loop is back at select, so the following
	// send proves the previous tick finished processing. No sleeping required.
	tick <- time.Time{}
	tick <- time.Time{}
	tick <- time.Time{}
	cancel()
	<-done

	// Then
	sends := telegram.callsFor("sendMessage")
	requireEqual(t, 1, len(sends))
	requireContains(t, sends[0].form.Get("text"), "Daily CPA Report")
	requireContains(t, sends[0].form.Get("text"), "CPA Usage Summary")
	requireEqual(t, "2026-08-21", service.state.LastReportDay)
}

func TestBotRun_suppressesCatchUpReport_whenStartedAfterScheduledTime(t *testing.T) {
	// Given
	telegram := newTelegramStub(t)
	cpa := newCPAStub(t)
	service := newTestBot(t, telegram, cpa, []int64{testChatID})
	service.now = func() time.Time { return time.Date(2026, 8, 21, 15, 0, 0, 0, time.UTC) }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	errRun := service.Run(ctx)

	// Then
	requireNoError(t, errRun)
	requireEqual(t, "2026-08-21", service.state.LastReportDay)
	requireEqual(t, 0, telegram.callCount())
}

func TestBotRun_resumesFromPersistedOffset(t *testing.T) {
	// Given
	telegram := newTelegramStub(t)
	cpa := newCPAStub(t)
	service := newTestBot(t, telegram, cpa, []int64{testChatID})
	requireNoError(t, saveBotState(service.config.statePath, botState{Offset: 99, LastReportDay: "2026-08-21"}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	errRun := service.Run(ctx)

	// Then
	requireNoError(t, errRun)
	requireEqual(t, int64(99), service.offset())
}

func TestBot_skipsDailyReport_whenScheduledTimeHasNotArrived(t *testing.T) {
	// Given
	telegram := newTelegramStub(t)
	cpa := newCPAStub(t)
	service := newTestBot(t, telegram, cpa, []int64{testChatID})
	service.config.reportAt = timeOfDay{hour: 23, minute: 30}

	// When
	service.maybeSendDailyReport(context.Background())

	// Then
	requireEqual(t, 0, telegram.callCount())
}

func TestParseCommand_recognisesSupportedCommands(t *testing.T) {
	// Given
	cases := map[string]string{
		"/usage":            "/usage",
		"/start@usage_bot":  "/start",
		"  /HELP  ":         "/help",
		"/usage extra args": "/usage",
		"hello":             "",
		"":                  "",
	}

	for input, want := range cases {
		// When
		got := parseCommand(input)

		// Then
		requireEqual(t, want, got)
	}
}
