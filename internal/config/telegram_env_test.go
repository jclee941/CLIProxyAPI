package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigAppliesTelegramEnvironment(t *testing.T) {
	// Given
	t.Setenv("TELEGRAM_BOT_TOKEN", "env-bot-token")
	t.Setenv("TELEGRAM_CHAT_ID", "-1004412765905")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if errWrite := os.WriteFile(configPath, []byte("telegram:\n  enabled: false\n"), 0o600); errWrite != nil {
		t.Fatalf("write config: %v", errWrite)
	}

	// When
	cfg, errLoad := LoadConfig(configPath)

	// Then
	if errLoad != nil {
		t.Fatalf("LoadConfig() error = %v", errLoad)
	}
	if !cfg.Telegram.Enabled {
		t.Fatal("Telegram.Enabled = false")
	}
	if cfg.Telegram.BotToken != "env-bot-token" {
		t.Fatalf("Telegram.BotToken = %q", cfg.Telegram.BotToken)
	}
	if cfg.Telegram.ChatID != "-1004412765905" {
		t.Fatalf("Telegram.ChatID = %q", cfg.Telegram.ChatID)
	}
}

func TestLoadConfigIgnoresUnresolvedTelegramSecretReferences(t *testing.T) {
	// Given
	t.Setenv("TELEGRAM_BOT_TOKEN", "op://homelab/telegram/credential")
	t.Setenv("TELEGRAM_CHAT_ID", "op://homelab/cliproxy/cliproxy_telegram_chid")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if errWrite := os.WriteFile(configPath, []byte("telegram:\n  enabled: false\n"), 0o600); errWrite != nil {
		t.Fatalf("write config: %v", errWrite)
	}

	// When
	cfg, errLoad := LoadConfig(configPath)

	// Then
	if errLoad != nil {
		t.Fatalf("LoadConfig() error = %v", errLoad)
	}
	if cfg.Telegram.Enabled {
		t.Fatal("Telegram.Enabled = true")
	}
}
