package main

import (
	"context"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	dayLayout           = "2006-01-02"
	reportCheckInterval = 30 * time.Second
	pollBackoff         = 5 * time.Second
)

// telegramAPI is the Bot API surface the bot depends on.
type telegramAPI interface {
	GetUpdates(ctx context.Context, offset int64) ([]telegramUpdate, error)
	SendMessage(ctx context.Context, chatID int64, text string, keyboard *inlineKeyboardMarkup) error
	EditMessageText(ctx context.Context, chatID, messageID int64, text string, keyboard *inlineKeyboardMarkup) error
	AnswerCallbackQuery(ctx context.Context, callbackID string) error
}

// usageSource is the CLIProxyAPI management surface the bot depends on.
type usageSource interface {
	FetchAPIKeyUsage(ctx context.Context) (apiKeyUsage, error)
	FetchStatus(ctx context.Context) (usageStatisticsStatus, error)
}

// bot long-polls Telegram and answers usage queries for whitelisted chats.
type bot struct {
	config     botConfig
	api        telegramAPI
	usage      usageSource
	allowed    map[int64]struct{}
	now        func() time.Time
	reportTick <-chan time.Time

	mu    sync.Mutex
	state botState
}

func newBot(config botConfig, api telegramAPI, usage usageSource) *bot {
	allowed := make(map[int64]struct{}, len(config.allowedChatIDs))
	for _, chatID := range config.allowedChatIDs {
		allowed[chatID] = struct{}{}
	}
	return &bot{config: config, api: api, usage: usage, allowed: allowed, now: time.Now}
}

// Run loads the persisted state and serves updates until the context is cancelled.
func (b *bot) Run(ctx context.Context) error {
	state, errLoad := loadBotState(b.config.statePath)
	if errLoad != nil {
		return errLoad
	}
	now := b.now()
	if state.LastReportDay == "" && minutesOfDay(now) >= b.config.reportAt.minutes() {
		state.LastReportDay = now.Format(dayLayout)
	}
	b.mu.Lock()
	b.state = state
	b.mu.Unlock()

	tick := b.reportTick
	if tick == nil {
		ticker := time.NewTicker(reportCheckInterval)
		defer ticker.Stop()
		tick = ticker.C
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.reportLoop(ctx, tick)
	}()
	b.pollLoop(ctx)
	<-done
	return nil
}

func (b *bot) pollLoop(ctx context.Context) {
	for ctx.Err() == nil {
		updates, errUpdates := b.api.GetUpdates(ctx, b.offset())
		if errUpdates != nil {
			if ctx.Err() != nil {
				return
			}
			log.WithError(errUpdates).Warn("telegram getUpdates failed")
			if !waitContext(ctx, pollBackoff) {
				return
			}
			continue
		}
		for _, update := range updates {
			b.handleUpdate(ctx, update)
			if errStore := b.storeOffset(update.UpdateID + 1); errStore != nil {
				log.WithError(errStore).Warn("failed to persist telegram update offset")
			}
		}
	}
}

func (b *bot) reportLoop(ctx context.Context, tick <-chan time.Time) {
	if !b.config.reportEnabled {
		<-ctx.Done()
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			b.maybeSendDailyReport(ctx)
		}
	}
}

// maybeSendDailyReport broadcasts the daily summary at most once per local day.
func (b *bot) maybeSendDailyReport(ctx context.Context) {
	now := b.now()
	if !b.reportDue(now) {
		return
	}
	text := dailyReportHeader(now) + b.renderAction(ctx, actionSummary)
	keyboard := usageKeyboard()
	for _, chatID := range b.config.allowedChatIDs {
		b.send(ctx, chatID, text, keyboard)
	}
	if errStore := b.storeReportDay(now.Format(dayLayout)); errStore != nil {
		log.WithError(errStore).Warn("failed to persist daily report marker")
	}
}

func (b *bot) isAllowed(chatID int64) bool {
	_, allowed := b.allowed[chatID]
	return allowed
}

func (b *bot) offset() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state.Offset
}

func (b *bot) storeOffset(offset int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if offset <= b.state.Offset {
		return nil
	}
	b.state.Offset = offset
	return saveBotState(b.config.statePath, b.state)
}

func (b *bot) storeReportDay(day string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state.LastReportDay = day
	return saveBotState(b.config.statePath, b.state)
}

func (b *bot) reportDue(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state.LastReportDay == now.Format(dayLayout) {
		return false
	}
	return minutesOfDay(now) >= b.config.reportAt.minutes()
}

func minutesOfDay(now time.Time) int {
	return now.Hour()*minutesPerHour + now.Minute()
}

func waitContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
