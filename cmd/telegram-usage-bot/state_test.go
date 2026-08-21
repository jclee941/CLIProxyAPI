package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBotState_roundTripsOffset_whenSavedAndReloaded(t *testing.T) {
	// Given
	statePath := filepath.Join(t.TempDir(), "nested", "telegram-usage-bot.json")

	// When
	requireNoError(t, saveBotState(statePath, botState{Offset: 4242, LastReportDay: "2026-08-21"}))
	reloaded, errLoad := loadBotState(statePath)

	// Then
	requireNoError(t, errLoad)
	requireEqual(t, int64(4242), reloaded.Offset)
	requireEqual(t, "2026-08-21", reloaded.LastReportDay)
}

func TestLoadBotState_returnsZeroState_whenFileIsMissing(t *testing.T) {
	// Given
	statePath := filepath.Join(t.TempDir(), "absent.json")

	// When
	state, errLoad := loadBotState(statePath)

	// Then
	requireNoError(t, errLoad)
	requireEqual(t, int64(0), state.Offset)
	requireEqual(t, "", state.LastReportDay)
}

func TestLoadBotState_failsLoudly_whenFileIsCorrupt(t *testing.T) {
	// Given
	statePath := filepath.Join(t.TempDir(), "corrupt.json")
	requireNoError(t, os.WriteFile(statePath, []byte("not-json"), 0o600))

	// When
	_, errLoad := loadBotState(statePath)

	// Then
	if errLoad == nil {
		t.Fatal("expected corrupt state to fail parsing")
	}
}

func TestStoreOffset_persistsMonotonicProgress(t *testing.T) {
	// Given
	telegram := newTelegramStub(t)
	cpa := newCPAStub(t)
	service := newTestBot(t, telegram, cpa, []int64{testChatID})

	// When
	requireNoError(t, service.storeOffset(10))
	requireNoError(t, service.storeOffset(4))
	persisted, errLoad := loadBotState(service.config.statePath)

	// Then
	requireNoError(t, errLoad)
	requireEqual(t, int64(10), persisted.Offset)
	requireEqual(t, int64(10), service.offset())
}
