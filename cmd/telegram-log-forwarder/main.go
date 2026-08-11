package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
)

func main() {
	logDir := flag.String("log-dir", envOrDefault("CPA_LOG_DIR", "/logs"), "CPA request log directory")
	statePath := flag.String("state-path", envOrDefault("STATE_PATH", "/state/delivered.json"), "delivery state file")
	pollInterval := flag.Duration("poll-interval", durationEnvOrDefault("POLL_INTERVAL", time.Second), "error log scan interval")
	settleDelay := flag.Duration("settle-delay", durationEnvOrDefault("SETTLE_DELAY", 2*time.Second), "minimum error log age before delivery")
	groupInterval := flag.Duration("group-interval", durationEnvOrDefault("GROUP_INTERVAL", 5*time.Minute), "minimum interval between matching alerts")
	flag.Parse()

	log.SetOutput(os.Stdout)
	log.SetFormatter(&log.JSONFormatter{})
	if errIntervals := validateIntervals(*pollInterval, *settleDelay, *groupInterval); errIntervals != nil {
		log.WithError(errIntervals).Error("invalid forwarder intervals")
		os.Exit(1)
	}
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_CHAT_ID")
	if token == "" || chatID == "" {
		log.Error("TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID are required")
		os.Exit(1)
	}
	instance := os.Getenv("INSTANCE_NAME")
	if instance == "" {
		hostname, errHostname := os.Hostname()
		if errHostname != nil {
			log.WithError(errHostname).Warn("failed to determine instance hostname")
			instance = "unknown"
		} else {
			instance = hostname
		}
	}

	sender := newTelegramSender(http.DefaultClient, telegramConfig{
		baseURL: envOrDefault("TELEGRAM_API_BASE_URL", "https://api.telegram.org"),
		token:   token,
		chatID:  chatID,
	})
	service := newForwarder(forwarderConfig{
		logDir:        *logDir,
		statePath:     *statePath,
		pollInterval:  *pollInterval,
		settleDelay:   *settleDelay,
		groupInterval: *groupInterval,
		environment:   envOrDefault("DEPLOY_ENV", "production"),
		instance:      instance,
		dashboardURL:  os.Getenv("DASHBOARD_URL"),
	}, sender)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if errRun := service.Run(ctx); errRun != nil {
		log.WithError(errRun).Error("telegram log forwarder stopped")
		os.Exit(1)
	}
}

func validateIntervals(pollInterval, settleDelay, groupInterval time.Duration) error {
	switch {
	case pollInterval <= 0:
		return fmt.Errorf("poll interval must be positive")
	case settleDelay < 0:
		return fmt.Errorf("settle delay must not be negative")
	case groupInterval <= 0:
		return fmt.Errorf("group interval must be positive")
	default:
		return nil
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func durationEnvOrDefault(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, errParse := time.ParseDuration(value)
	if errParse != nil {
		return fallback
	}
	return parsed
}
