package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

type alertSender interface {
	Send(context.Context, errorAlert) error
}

type forwarderConfig struct {
	logDir        string
	statePath     string
	pollInterval  time.Duration
	settleDelay   time.Duration
	groupInterval time.Duration
	environment   string
	instance      string
	dashboardURL  string
}

type forwarder struct {
	config forwarderConfig
	sender alertSender
	state  forwarderState
	loaded bool
}

func newForwarder(config forwarderConfig, sender alertSender) *forwarder {
	return &forwarder{config: config, sender: sender}
}

func (f *forwarder) Run(ctx context.Context) error {
	if errScan := f.scan(ctx, time.Now()); errScan != nil {
		return errScan
	}
	ticker := time.NewTicker(f.config.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if errScan := f.scan(ctx, now); errScan != nil {
				log.WithError(errScan).Warn("telegram log scan failed")
			}
		}
	}
}

func (f *forwarder) scan(ctx context.Context, now time.Time) error {
	entries, errReadDir := os.ReadDir(f.config.logDir)
	if errReadDir != nil {
		return fmt.Errorf("read CPA log directory: %w", errReadDir)
	}
	stateChanged := false
	if !f.loaded {
		state, found, errLoad := loadDeliveryState(f.config.statePath)
		if errLoad != nil {
			return errLoad
		}
		f.state = state
		f.loaded = true
		if !found {
			for _, entry := range entries {
				if isErrorLog(entry) {
					f.state.Seen[entry.Name()] = struct{}{}
				}
			}
			return saveDeliveryState(f.config.statePath, f.state)
		}
		stateChanged = true
	}
	if f.state.pruneGroups(now) {
		stateChanged = true
	}

	current := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if !isErrorLog(entry) {
			continue
		}
		current[entry.Name()] = struct{}{}
		if _, delivered := f.state.Seen[entry.Name()]; delivered {
			continue
		}
		info, errInfo := entry.Info()
		if errInfo != nil {
			return fmt.Errorf("stat CPA error log %s: %w", entry.Name(), errInfo)
		}
		if now.Sub(info.ModTime()) < f.config.settleDelay {
			continue
		}
		alert, errParse := parseErrorLog(filepath.Join(f.config.logDir, entry.Name()))
		if errors.Is(errParse, errIncompleteLog) {
			continue
		}
		if errParse != nil {
			log.WithError(errParse).WithField("file", entry.Name()).Error("failed to parse CPA error log")
			continue
		}
		if alert.OccurredAt.Before(now.Add(-24 * time.Hour)) {
			f.state.Seen[entry.Name()] = struct{}{}
			stateChanged = true
			continue
		}
		alert.Environment = f.config.environment
		alert.Instance = f.config.instance
		alert.DashboardURL = f.config.dashboardURL
		fingerprint := alertFingerprint(alert)
		group, groupExists := f.state.Groups[fingerprint]
		group = group.recordOccurrence(alert.OccurredAt, now)
		alert.Occurrences24h = group.occurrenceCount()
		deliver := group.LastSent.IsZero() || now.Sub(group.LastSent) >= f.config.groupInterval
		if deliver {
			if errSend := f.sender.Send(ctx, alert); errSend != nil {
				var apiError *telegramAPIError
				permanent := errors.As(errSend, &apiError) && apiError.isPermanentMessageFailure()
				if permanent {
					f.state.Seen[entry.Name()] = struct{}{}
					stateChanged = true
					log.WithError(errSend).WithField("file", entry.Name()).Error("Telegram permanently rejected CPA error alert")
					continue
				}
				if stateChanged {
					if errSave := saveDeliveryState(f.config.statePath, f.state); errSave != nil {
						return errSave
					}
				}
				return fmt.Errorf("send Telegram alert for %s: %w", entry.Name(), errSend)
			}
			group.LastSent = now
		}
		if !groupExists {
			before := len(f.state.Groups)
			f.state.makeRoomForGroup()
			stateChanged = stateChanged || len(f.state.Groups) != before
		}
		f.state.Groups[fingerprint] = group
		f.state.Seen[entry.Name()] = struct{}{}
		stateChanged = true
		if deliver {
			log.WithFields(log.Fields{
				"count_24h": alert.Occurrences24h,
				"file":      entry.Name(),
				"route":     alert.Route,
				"status":    alert.Status,
			}).Info("CPA error alert delivered")
		}
	}

	for name := range f.state.Seen {
		if _, exists := current[name]; !exists {
			delete(f.state.Seen, name)
			stateChanged = true
		}
	}
	if stateChanged {
		return saveDeliveryState(f.config.statePath, f.state)
	}
	return nil
}

func isErrorLog(entry os.DirEntry) bool {
	return !entry.IsDir() && strings.HasPrefix(entry.Name(), "error-") && strings.HasSuffix(entry.Name(), ".log")
}
