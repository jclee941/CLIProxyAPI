package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigOptional_normalizes_telegram_notifications(t *testing.T) {
	// Given
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configYAML := []byte(`
telegram:
  enabled: true
  bot-token: "  test-bot-token  "
  chat-id: "  -1001234567890  "
`)
	if err := os.WriteFile(configPath, configYAML, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// When
	cfg, err := LoadConfigOptional(configPath, false)

	// Then
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if !cfg.Telegram.Enabled {
		t.Fatal("Telegram.Enabled = false, want true")
	}
	if got := cfg.Telegram.BotToken; got != "test-bot-token" {
		t.Fatalf("Telegram.BotToken = %q, want %q", got, "test-bot-token")
	}
	if got := cfg.Telegram.ChatID; got != "-1001234567890" {
		t.Fatalf("Telegram.ChatID = %q, want %q", got, "-1001234567890")
	}
}

func TestLoadConfigOptional_rejects_enabled_telegram_without_credentials(t *testing.T) {
	// Given
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configYAML := []byte(`
telegram:
  enabled: true
  bot-token: ""
  chat-id: "-1001234567890"
`)
	if err := os.WriteFile(configPath, configYAML, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// When
	_, err := LoadConfigOptional(configPath, false)

	// Then
	if err == nil {
		t.Fatal("LoadConfigOptional() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "telegram bot-token and chat-id are required") {
		t.Fatalf("LoadConfigOptional() error = %q, want Telegram credential validation error", err)
	}
}
