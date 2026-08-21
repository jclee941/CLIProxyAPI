package main

import (
	"context"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	actionSummary   = "usage:summary"
	actionProviders = "usage:providers"
	actionFailures  = "usage:failures"
	actionPing      = "status:ping"
)

func (b *bot) handleUpdate(ctx context.Context, update telegramUpdate) {
	switch {
	case update.CallbackQuery != nil:
		b.handleCallback(ctx, update.CallbackQuery)
	case update.Message != nil:
		b.handleMessage(ctx, update.Message)
	default:
		log.WithField("update_id", update.UpdateID).Debug("ignoring unsupported telegram update")
	}
}

func (b *bot) handleMessage(ctx context.Context, message *telegramMessage) {
	chatID := message.Chat.ID
	if !b.isAllowed(chatID) {
		log.WithField("chat_id", chatID).Warn("ignoring telegram message from non-whitelisted chat")
		return
	}
	command := parseCommand(message.Text)
	if command == "" {
		return
	}
	text := b.renderAction(ctx, actionSummary)
	if command == "/help" {
		text = helpText() + "\n" + text
	}
	b.send(ctx, chatID, text, usageKeyboard())
}

func (b *bot) handleCallback(ctx context.Context, callback *telegramCallbackQuery) {
	var chatID, messageID int64
	if callback.Message != nil {
		chatID = callback.Message.Chat.ID
		messageID = callback.Message.MessageID
	}
	if !b.isAllowed(chatID) {
		log.WithField("chat_id", chatID).Warn("ignoring telegram callback from non-whitelisted chat")
		return
	}
	if errAnswer := b.api.AnswerCallbackQuery(ctx, callback.ID); errAnswer != nil {
		log.WithError(errAnswer).Warn("failed to answer telegram callback query")
	}
	text := b.renderAction(ctx, callback.Data)
	keyboard := usageKeyboard()
	if messageID == 0 {
		b.send(ctx, chatID, text, keyboard)
		return
	}
	if errEdit := b.api.EditMessageText(ctx, chatID, messageID, text, keyboard); errEdit != nil {
		log.WithError(errEdit).Warn("failed to edit telegram message; falling back to sendMessage")
		b.send(ctx, chatID, text, keyboard)
	}
}

// renderAction resolves a callback action into HTML content, degrading when CPA is down.
func (b *bot) renderAction(ctx context.Context, action string) string {
	switch action {
	case actionPing:
		status, errStatus := b.usage.FetchStatus(ctx)
		if errStatus != nil {
			log.WithError(errStatus).Warn("CPA status check failed")
			return renderUnavailable(errStatus)
		}
		return renderStatus(b.config.cpaBaseURL, status, b.now())
	case actionSummary, actionProviders, actionFailures:
		usage, errUsage := b.usage.FetchAPIKeyUsage(ctx)
		if errUsage != nil {
			log.WithError(errUsage).Warn("CPA api key usage fetch failed")
			return renderUnavailable(errUsage)
		}
		return renderUsageAction(action, summarizeUsage(usage), b.now())
	default:
		return renderUnknownAction(action)
	}
}

func renderUsageAction(action string, report usageReport, now time.Time) string {
	switch action {
	case actionProviders:
		return renderProviders(report)
	case actionFailures:
		return renderFailures(report)
	default:
		return renderSummary(report, now)
	}
}

func (b *bot) send(ctx context.Context, chatID int64, text string, keyboard *inlineKeyboardMarkup) {
	if errSend := b.api.SendMessage(ctx, chatID, text, keyboard); errSend != nil {
		log.WithError(errSend).WithField("chat_id", chatID).Warn("failed to send telegram message")
	}
}

// usageKeyboard is the inline keyboard attached to every bot message.
func usageKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{InlineKeyboard: [][]inlineKeyboardButton{
		{
			{Text: "Summary", CallbackData: actionSummary},
			{Text: "Providers", CallbackData: actionProviders},
		},
		{
			{Text: "Failures", CallbackData: actionFailures},
			{Text: "Status", CallbackData: actionPing},
		},
	}}
}

// parseCommand extracts a supported slash command, tolerating the @botname suffix.
func parseCommand(text string) string {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return ""
	}
	command := strings.ToLower(fields[0])
	if at := strings.Index(command, "@"); at > 0 {
		command = command[:at]
	}
	switch command {
	case "/start", "/usage", "/help":
		return command
	default:
		return ""
	}
}
