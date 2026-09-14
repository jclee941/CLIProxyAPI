package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	log "github.com/sirupsen/logrus"
)

const (
	telegramAPIBaseURL    = "https://api.telegram.org"
	telegramQueueCapacity = 64
	telegramTextLimit     = 200
	telegramRequestLimit  = 10 * time.Second
	telegramResponseLimit = 64 << 10
)

var (
	safeTelegramMetadataPattern = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,128}$`)
	telegramSecretPattern       = regexp.MustCompile(`(?i)\b(authorization|api[_-]?key|token|secret|password)\s*[:=]\s*(?:bearer\s+)?[^\s,;]+`)
	telegramBearerPattern       = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`)
	telegramAPIKeyPattern       = regexp.MustCompile(`\b(?:sk|key)-[A-Za-z0-9_-]{12,}\b`)
	telegramBotTokenPattern     = regexp.MustCompile(`\b[0-9]{6,}:[A-Za-z0-9_-]{20,}\b`)
	globalTelegramHook          = newTelegramHook(http.DefaultClient, telegramAPIBaseURL)
)

type telegramAlert struct {
	text string
}

type telegramWorker struct {
	client   *http.Client
	baseURL  string
	botToken string
	chatID   string
	queue    chan telegramAlert
	cancel   context.CancelFunc
	done     chan struct{}
}

type telegramHook struct {
	mu      sync.RWMutex
	client  *http.Client
	baseURL string
	config  config.TelegramConfig
	worker  *telegramWorker
}

type telegramSendMessage struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

type telegramResponse struct {
	OK        bool `json:"ok"`
	ErrorCode int  `json:"error_code"`
}

func newTelegramHook(client *http.Client, baseURL string) *telegramHook {
	return &telegramHook{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

func (h *telegramHook) Levels() []log.Level {
	return []log.Level{log.ErrorLevel}
}

func (h *telegramHook) Fire(entry *log.Entry) error {
	if entry == nil {
		return nil
	}
	alert := telegramAlert{text: formatTelegramAlert(entry)}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.worker == nil {
		return nil
	}
	select {
	case h.worker.queue <- alert:
	default:
		log.Warn("telegram notification queue full; dropping alert")
	}
	return nil
}

func (h *telegramHook) Configure(cfg config.TelegramConfig) {
	h.mu.Lock()
	if h.config == cfg {
		h.mu.Unlock()
		return
	}
	oldWorker := h.worker
	h.config = cfg
	h.worker = nil
	if cfg.Enabled && cfg.BotToken != "" && cfg.ChatID != "" {
		ctx, cancel := context.WithCancel(context.Background())
		worker := &telegramWorker{
			client:   h.client,
			baseURL:  h.baseURL,
			botToken: cfg.BotToken,
			chatID:   cfg.ChatID,
			queue:    make(chan telegramAlert, telegramQueueCapacity),
			cancel:   cancel,
			done:     make(chan struct{}),
		}
		h.worker = worker
		go worker.run(ctx)
	}
	h.mu.Unlock()
	stopTelegramWorker(oldWorker)
}

func (h *telegramHook) Close() {
	h.mu.Lock()
	worker := h.worker
	h.config = config.TelegramConfig{}
	h.worker = nil
	h.mu.Unlock()
	stopTelegramWorker(worker)
}

func stopTelegramWorker(worker *telegramWorker) {
	if worker == nil {
		return
	}
	worker.cancel()
	<-worker.done
}

func (w *telegramWorker) run(ctx context.Context) {
	defer close(w.done)
	for {
		select {
		case <-ctx.Done():
			return
		case alert := <-w.queue:
			if err := w.send(ctx, alert); err != nil {
				log.Warn("telegram notification delivery failed")
			}
		}
	}
}

func (w *telegramWorker) send(ctx context.Context, alert telegramAlert) error {
	payload, err := json.Marshal(telegramSendMessage{ChatID: w.chatID, Text: alert.text, ParseMode: "HTML"})
	if err != nil {
		return fmt.Errorf("marshal Telegram notification: %w", err)
	}
	endpoint := w.baseURL + "/bot" + url.PathEscape(w.botToken) + "/sendMessage"
	requestCtx, cancel := context.WithTimeout(ctx, telegramRequestLimit)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create Telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("send Telegram request: %w", err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Warn("telegram notification response close failed")
		}
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, telegramResponseLimit))
	if err != nil {
		return fmt.Errorf("read Telegram response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Telegram API returned status %d", resp.StatusCode)
	}
	var result telegramResponse
	if err = json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("decode Telegram response: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("Telegram API rejected notification with code %d", result.ErrorCode)
	}
	return nil
}

func formatTelegramAlert(entry *log.Entry) string {
	lines := []string{"<b>CLIProxyAPI · Model request failed</b>"}
	summary := make([]string, 0, 3)
	for _, key := range []string{"provider", "model"} {
		if value := telegramMetadata(entry, key); value != "" {
			summary = append(summary, value)
		}
	}
	if status := telegramMetadata(entry, "status"); status != "" {
		summary = append(summary, "HTTP "+status)
	}
	if len(summary) == 0 {
		summary = append(summary, strings.ToUpper(entry.Level.String()))
	}
	lines = append(lines, "<code>"+html.EscapeString(strings.Join(summary, " · "))+"</code>")

	causeText := ""
	if cause, ok := entry.Data[log.ErrorKey]; ok {
		causeText = telegramHTMLText(fmt.Sprint(cause))
	}
	if causeText != "" {
		lines = append(lines, "", "<b>Error</b>", "<pre>"+causeText+"</pre>")
	}
	messageText := telegramHTMLText(entry.Message)
	if messageText != "" && (causeText == "" || entry.Message != "upstream model request failed") {
		lines = append(lines, "", "<b>Message</b>", "<pre>"+messageText+"</pre>")
	}

	details := make([]string, 0, 3)
	if requestID := telegramMetadata(entry, "request_id"); requestID != "" {
		details = append(details, "<b>Request</b>  <code>"+requestID+"</code>")
	}
	details = append(details, "<b>Time</b>     <code>"+entry.Time.UTC().Format("2006-01-02 15:04:05 UTC")+"</code>")
	if entry.Caller != nil {
		source := fmt.Sprintf("%s:%d", filepath.Base(entry.Caller.File), entry.Caller.Line)
		if safeTelegramMetadataPattern.MatchString(source) {
			details = append(details, "<b>Source</b>   <code>"+source+"</code>")
		}
	}
	if len(details) > 0 {
		lines = append(lines, "", "<b>Details</b>")
		lines = append(lines, details...)
	}
	return strings.Join(lines, "\n")
}

func redactTelegramText(value string) string {
	redacted := telegramSecretPattern.ReplaceAllString(value, `${1}=<redacted>`)
	redacted = telegramBearerPattern.ReplaceAllString(redacted, "Bearer <redacted>")
	redacted = telegramAPIKeyPattern.ReplaceAllString(redacted, "<redacted>")
	redacted = telegramBotTokenPattern.ReplaceAllString(redacted, "<redacted>")
	return strings.TrimSpace(strings.ReplaceAll(redacted, "\r\n", "\n"))
}

func telegramMetadata(entry *log.Entry, key string) string {
	value, ok := entry.Data[key].(string)
	if !ok || !safeTelegramMetadataPattern.MatchString(value) {
		return ""
	}
	return value
}

func telegramHTMLText(value string) string {
	redacted := redactTelegramText(value)
	if utf8.RuneCountInString(redacted) > telegramTextLimit {
		runes := []rune(redacted)
		redacted = string(runes[:telegramTextLimit-3]) + "..."
	}
	return html.EscapeString(redacted)
}
