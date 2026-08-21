package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// botState is the durable long-poll and daily-report bookkeeping.
type botState struct {
	Offset        int64  `json:"offset"`
	LastReportDay string `json:"last_report_day,omitempty"`
}

// loadBotState reads the state file, returning a zero state when it is absent.
func loadBotState(path string) (botState, error) {
	data, errRead := os.ReadFile(path)
	if errors.Is(errRead, os.ErrNotExist) {
		return botState{}, nil
	}
	if errRead != nil {
		return botState{}, fmt.Errorf("read bot state: %w", errRead)
	}
	var state botState
	if errUnmarshal := json.Unmarshal(data, &state); errUnmarshal != nil {
		return botState{}, fmt.Errorf("parse bot state: %w", errUnmarshal)
	}
	return state, nil
}

// saveBotState atomically persists the state file.
func saveBotState(path string, state botState) error {
	data, errMarshal := json.Marshal(state)
	if errMarshal != nil {
		return fmt.Errorf("encode bot state: %w", errMarshal)
	}
	if errMkdir := os.MkdirAll(filepath.Dir(path), 0o700); errMkdir != nil {
		return fmt.Errorf("create state directory: %w", errMkdir)
	}
	tempPath := path + ".tmp"
	if errWrite := os.WriteFile(tempPath, data, 0o600); errWrite != nil {
		return fmt.Errorf("write bot state: %w", errWrite)
	}
	if errRename := os.Rename(tempPath, path); errRename != nil {
		return fmt.Errorf("replace bot state: %w", errRename)
	}
	return nil
}
