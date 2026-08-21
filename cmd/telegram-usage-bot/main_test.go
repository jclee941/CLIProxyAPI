package main

import (
	"testing"
	"time"
)

func TestParseChatIDs_parsesAndDeduplicatesWhitelist(t *testing.T) {
	// Given / When
	chatIDs, errParse := parseChatIDs(" -100123 , 456 ,-100123, ")

	// Then
	requireNoError(t, errParse)
	requireEqual(t, 2, len(chatIDs))
	requireEqual(t, int64(-100123), chatIDs[0])
	requireEqual(t, int64(456), chatIDs[1])
}

func TestParseChatIDs_rejectsEmptyAndInvalidInput(t *testing.T) {
	// Given / When
	_, errEmpty := parseChatIDs("  ")
	_, errInvalid := parseChatIDs("123,abc")

	// Then
	if errEmpty == nil {
		t.Fatal("expected empty whitelist to be rejected")
	}
	if errInvalid == nil {
		t.Fatal("expected non-numeric chat id to be rejected")
	}
}

func TestParseTimeOfDay_parsesLocalScheduleSlot(t *testing.T) {
	// Given / When
	parsed, errParse := parseTimeOfDay(" 21:05 ")

	// Then
	requireNoError(t, errParse)
	requireEqual(t, 21, parsed.hour)
	requireEqual(t, 5, parsed.minute)
	requireEqual(t, 1265, parsed.minutes())
}

func TestParseTimeOfDay_rejectsOutOfRangeValues(t *testing.T) {
	// Given
	invalid := []string{"", "9", "24:00", "09:60", "aa:bb", "09:00:00"}

	for _, value := range invalid {
		// When
		_, errParse := parseTimeOfDay(value)

		// Then
		if errParse == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}

func TestParsePollTimeout_usesDefaultAndRejectsNonPositive(t *testing.T) {
	// Given / When
	fallback, errFallback := parsePollTimeout("")
	custom, errCustom := parsePollTimeout("45")
	_, errZero := parsePollTimeout("0")

	// Then
	requireNoError(t, errFallback)
	requireNoError(t, errCustom)
	requireEqual(t, 30*time.Second, fallback)
	requireEqual(t, 45*time.Second, custom)
	if errZero == nil {
		t.Fatal("expected zero poll timeout to be rejected")
	}
}

func TestParseBool_usesFallbackWhenUnset(t *testing.T) {
	// Given / When
	fallback, errFallback := parseBool("", true)
	disabled, errDisabled := parseBool("false", true)
	_, errInvalid := parseBool("maybe", true)

	// Then
	requireNoError(t, errFallback)
	requireNoError(t, errDisabled)
	requireEqual(t, true, fallback)
	requireEqual(t, false, disabled)
	if errInvalid == nil {
		t.Fatal("expected non-boolean REPORT_ENABLED to be rejected")
	}
}

func TestLoadBotConfig_appliesDefaults_whenOptionalEnvIsUnset(t *testing.T) {
	// Given
	t.Setenv("TELEGRAM_BOT_TOKEN", testBotToken)
	t.Setenv("CPA_MANAGEMENT_KEY", testManagementKey)
	t.Setenv("TELEGRAM_ALLOWED_CHAT_IDS", "-100123")
	t.Setenv("CPA_BASE_URL", "")
	t.Setenv("TELEGRAM_API_BASE_URL", "")
	t.Setenv("TELEGRAM_POLL_TIMEOUT_SECONDS", "")
	t.Setenv("REPORT_TIME", "")
	t.Setenv("REPORT_ENABLED", "")
	t.Setenv("STATE_PATH", "")

	// When
	config, errConfig := loadBotConfig()

	// Then
	requireNoError(t, errConfig)
	requireEqual(t, defaultCPABaseURL, config.cpaBaseURL)
	requireEqual(t, defaultTelegramBaseURL, config.telegram.baseURL)
	requireEqual(t, 30*time.Second, config.telegram.pollTimeout)
	requireEqual(t, defaultStatePath, config.statePath)
	requireEqual(t, 9, config.reportAt.hour)
	requireEqual(t, true, config.reportEnabled)
	requireEqual(t, 1, len(config.allowedChatIDs))
}

func TestLoadBotConfig_requiresTokenAndManagementKey(t *testing.T) {
	// Given
	t.Setenv("TELEGRAM_ALLOWED_CHAT_IDS", "-100123")
	t.Setenv("CPA_MANAGEMENT_KEY", testManagementKey)
	t.Setenv("TELEGRAM_BOT_TOKEN", "")

	// When
	_, errMissingToken := loadBotConfig()
	t.Setenv("TELEGRAM_BOT_TOKEN", testBotToken)
	t.Setenv("CPA_MANAGEMENT_KEY", "")
	_, errMissingKey := loadBotConfig()

	// Then
	if errMissingToken == nil {
		t.Fatal("expected missing TELEGRAM_BOT_TOKEN to be rejected")
	}
	if errMissingKey == nil {
		t.Fatal("expected missing CPA_MANAGEMENT_KEY to be rejected")
	}
}
