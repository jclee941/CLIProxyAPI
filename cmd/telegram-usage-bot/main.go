package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	// cpaRequestTimeout bounds management API calls; this command is a standalone
	// utility, so short-lived request deadlines are intentional here.
	cpaRequestTimeout = 15 * time.Second
	// telegramCallTimeout bounds every non long-polling Bot API call.
	telegramCallTimeout = 30 * time.Second

	defaultCPABaseURL      = "http://127.0.0.1:8317"
	defaultTelegramBaseURL = "https://api.telegram.org"
	defaultPollSeconds     = 30
	defaultReportTime      = "09:00"
	defaultStatePath       = "./state/telegram-usage-bot.json"

	minutesPerHour = 60
	hoursPerDay    = 24
)

// timeOfDay is a local wall-clock HH:MM schedule slot.
type timeOfDay struct {
	hour   int
	minute int
}

func (t timeOfDay) minutes() int {
	return t.hour*minutesPerHour + t.minute
}

// botConfig is the fully validated runtime configuration.
type botConfig struct {
	telegram       telegramConfig
	cpaBaseURL     string
	managementKey  string
	allowedChatIDs []int64
	reportAt       timeOfDay
	reportEnabled  bool
	statePath      string
}

func main() {
	log.SetOutput(os.Stdout)
	log.SetFormatter(&log.JSONFormatter{})

	config, errConfig := loadBotConfig()
	if errConfig != nil {
		log.WithError(errConfig).Error("invalid telegram usage bot configuration")
		os.Exit(1)
	}

	client := &http.Client{}
	service := newBot(
		config,
		newTelegramClient(client, config.telegram),
		newCPAClient(client, config.cpaBaseURL, config.managementKey, cpaRequestTimeout),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.WithFields(log.Fields{
		"cpa_base_url": config.cpaBaseURL,
		"chats":        len(config.allowedChatIDs),
		"report_at":    fmt.Sprintf("%02d:%02d", config.reportAt.hour, config.reportAt.minute),
	}).Info("telegram usage bot started")
	if errRun := service.Run(ctx); errRun != nil {
		log.WithError(errRun).Error("telegram usage bot stopped")
		os.Exit(1)
	}
}

func loadBotConfig() (botConfig, error) {
	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if token == "" {
		return botConfig{}, errors.New("TELEGRAM_BOT_TOKEN is required")
	}
	managementKey := strings.TrimSpace(os.Getenv("CPA_MANAGEMENT_KEY"))
	if managementKey == "" {
		return botConfig{}, errors.New("CPA_MANAGEMENT_KEY is required")
	}
	chatIDs, errChats := parseChatIDs(os.Getenv("TELEGRAM_ALLOWED_CHAT_IDS"))
	if errChats != nil {
		return botConfig{}, errChats
	}
	pollTimeout, errPoll := parsePollTimeout(os.Getenv("TELEGRAM_POLL_TIMEOUT_SECONDS"))
	if errPoll != nil {
		return botConfig{}, errPoll
	}
	reportAt, errReport := parseTimeOfDay(envOrDefault("REPORT_TIME", defaultReportTime))
	if errReport != nil {
		return botConfig{}, errReport
	}
	reportEnabled, errEnabled := parseBool(os.Getenv("REPORT_ENABLED"), true)
	if errEnabled != nil {
		return botConfig{}, errEnabled
	}
	return botConfig{
		telegram: telegramConfig{
			baseURL:     envOrDefault("TELEGRAM_API_BASE_URL", defaultTelegramBaseURL),
			token:       token,
			pollTimeout: pollTimeout,
			callTimeout: telegramCallTimeout,
		},
		cpaBaseURL:     envOrDefault("CPA_BASE_URL", defaultCPABaseURL),
		managementKey:  managementKey,
		allowedChatIDs: chatIDs,
		reportAt:       reportAt,
		reportEnabled:  reportEnabled,
		statePath:      envOrDefault("STATE_PATH", defaultStatePath),
	}, nil
}

func parseChatIDs(value string) ([]int64, error) {
	fields := strings.Split(value, ",")
	chatIDs := make([]int64, 0, len(fields))
	seen := make(map[int64]struct{}, len(fields))
	for _, field := range fields {
		trimmed := strings.TrimSpace(field)
		if trimmed == "" {
			continue
		}
		chatID, errParse := strconv.ParseInt(trimmed, 10, 64)
		if errParse != nil {
			return nil, fmt.Errorf("invalid chat id %q in TELEGRAM_ALLOWED_CHAT_IDS", trimmed)
		}
		if _, exists := seen[chatID]; exists {
			continue
		}
		seen[chatID] = struct{}{}
		chatIDs = append(chatIDs, chatID)
	}
	if len(chatIDs) == 0 {
		return nil, errors.New("TELEGRAM_ALLOWED_CHAT_IDS must list at least one chat id")
	}
	return chatIDs, nil
}

func parsePollTimeout(value string) (time.Duration, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return defaultPollSeconds * time.Second, nil
	}
	seconds, errParse := strconv.Atoi(trimmed)
	if errParse != nil || seconds <= 0 {
		return 0, fmt.Errorf("TELEGRAM_POLL_TIMEOUT_SECONDS must be a positive integer, got %q", trimmed)
	}
	return time.Duration(seconds) * time.Second, nil
}

func parseTimeOfDay(value string) (timeOfDay, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return timeOfDay{}, fmt.Errorf("REPORT_TIME must use HH:MM, got %q", value)
	}
	hour, errHour := strconv.Atoi(strings.TrimSpace(parts[0]))
	minute, errMinute := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errHour != nil || errMinute != nil || hour < 0 || hour >= hoursPerDay || minute < 0 || minute >= minutesPerHour {
		return timeOfDay{}, fmt.Errorf("REPORT_TIME must use HH:MM, got %q", value)
	}
	return timeOfDay{hour: hour, minute: minute}, nil
}

func parseBool(value string, fallback bool) (bool, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback, nil
	}
	parsed, errParse := strconv.ParseBool(trimmed)
	if errParse != nil {
		return false, fmt.Errorf("REPORT_ENABLED must be a boolean, got %q", trimmed)
	}
	return parsed, nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
