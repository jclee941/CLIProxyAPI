package config

import (
	"fmt"
	"os"
	"strings"
)

// TelegramConfig controls Telegram notifications for error log entries.
type TelegramConfig struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	BotToken string `yaml:"bot-token" json:"-"`
	ChatID   string `yaml:"chat-id" json:"chat-id"`
}

func (c *TelegramConfig) applyEnvironment() {
	if botToken := resolvedTelegramEnvironmentValue("TELEGRAM_BOT_TOKEN"); botToken != "" {
		c.BotToken = botToken
	}
	if chatID := resolvedTelegramEnvironmentValue("TELEGRAM_CHAT_ID"); chatID != "" {
		c.ChatID = chatID
	}
	if c.BotToken != "" && c.ChatID != "" {
		c.Enabled = true
	}
}

func resolvedTelegramEnvironmentValue(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if strings.HasPrefix(value, "op://") {
		return ""
	}
	return value
}

func (c *TelegramConfig) normalize() error {
	c.BotToken = strings.TrimSpace(c.BotToken)
	c.ChatID = strings.TrimSpace(c.ChatID)
	if !c.Enabled {
		return nil
	}
	if c.BotToken == "" || c.ChatID == "" {
		return fmt.Errorf("telegram bot-token and chat-id are required when notifications are enabled")
	}
	return nil
}
