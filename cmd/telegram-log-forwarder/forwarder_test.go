package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type recordingSender struct {
	alerts []errorAlert
	err    error
	calls  int
}

func (s *recordingSender) Send(_ context.Context, alert errorAlert) error {
	s.calls++
	if s.err != nil {
		return s.err
	}
	s.alerts = append(s.alerts, alert)
	return nil
}

func TestForwarderScan_sendsOnlyNewErrors_andPersistsDeliveryState(t *testing.T) {
	// Given
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	requireNoError(t, os.Mkdir(logDir, 0o700))
	statePath := filepath.Join(root, "state", "seen.json")
	existingPath := filepath.Join(logDir, "error-existing.log")
	requireNoError(t, writeCompleteTestLog(existingPath, 400, "existing error"))
	sender := &recordingSender{}
	forwarder := newForwarder(forwarderConfig{
		logDir:      logDir,
		statePath:   statePath,
		settleDelay: 0,
	}, sender)
	now := time.Now().Add(time.Second)

	// When: the first scan establishes a baseline.
	requireNoError(t, forwarder.scan(context.Background(), now))

	// Then
	requireEqual(t, 0, len(sender.alerts))

	// When: CPA writes a new complete error log.
	newPath := filepath.Join(logDir, "error-new.log")
	requireNoError(t, writeCompleteTestLog(newPath, 502, "upstream unavailable"))
	requireNoError(t, forwarder.scan(context.Background(), now.Add(time.Second)))

	// Then
	requireEqual(t, 1, len(sender.alerts))
	requireEqual(t, 502, sender.alerts[0].Status)

	// When: the same process and then a restarted process scan again.
	requireNoError(t, forwarder.scan(context.Background(), now.Add(2*time.Second)))
	restartedSender := &recordingSender{}
	restarted := newForwarder(forwarderConfig{
		logDir:      logDir,
		statePath:   statePath,
		settleDelay: 0,
	}, restartedSender)
	requireNoError(t, restarted.scan(context.Background(), now.Add(3*time.Second)))

	// Then
	requireEqual(t, 1, len(sender.alerts))
	requireEqual(t, 0, len(restartedSender.alerts))
}

func TestForwarderScan_groupsDuplicateErrors_andCountsOccurrencesFor24Hours(t *testing.T) {
	// Given
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	requireNoError(t, os.Mkdir(logDir, 0o700))
	statePath := filepath.Join(root, "state", "seen.json")
	sender := &recordingSender{}
	service := newForwarder(forwarderConfig{
		logDir:        logDir,
		statePath:     statePath,
		settleDelay:   0,
		groupInterval: 5 * time.Minute,
	}, sender)
	now := time.Now().Add(time.Second)
	requireNoError(t, service.scan(context.Background(), now))

	// When: four matching errors arrive across one grouping interval.
	for index, elapsed := range []time.Duration{time.Second, time.Minute, 2 * time.Minute, 6 * time.Minute} {
		path := filepath.Join(logDir, "error-duplicate-"+statusText(index)+".log")
		requireNoError(t, writeCompleteTestLog(path, 502, "upstream unavailable"))
		requireNoError(t, service.scan(context.Background(), now.Add(elapsed)))
	}

	// Then
	requireEqual(t, 2, len(sender.alerts))
	requireEqual(t, 1, sender.alerts[0].Occurrences24h)
	requireEqual(t, 4, sender.alerts[1].Occurrences24h)
}

func TestErrorGroup_recordOccurrence_prunesEventsOlderThan24Hours(t *testing.T) {
	// Given
	now := time.Now()
	group := errorGroup{}

	// When
	stale := group.recordOccurrence(now.Add(-25*time.Hour), now)
	recent := stale.recordOccurrence(now.Add(-23*time.Hour), now)
	got := recent.recordOccurrence(now, now)

	// Then
	requireEqual(t, 0, stale.occurrenceCount())
	requireEqual(t, 2, got.occurrenceCount())
}

func TestForwarderScan_persistsGroupingState_acrossRestart(t *testing.T) {
	// Given
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	requireNoError(t, os.Mkdir(logDir, 0o700))
	statePath := filepath.Join(root, "state", "seen.json")
	now := time.Now().Add(time.Second)
	firstSender := &recordingSender{}
	first := newForwarder(forwarderConfig{
		logDir:        logDir,
		statePath:     statePath,
		settleDelay:   0,
		groupInterval: 5 * time.Minute,
	}, firstSender)
	requireNoError(t, first.scan(context.Background(), now))
	requireNoError(t, writeCompleteTestLog(filepath.Join(logDir, "error-first.log"), 500, "same failure"))
	requireNoError(t, first.scan(context.Background(), now.Add(time.Second)))

	// When
	restartedSender := &recordingSender{}
	restarted := newForwarder(forwarderConfig{
		logDir:        logDir,
		statePath:     statePath,
		settleDelay:   0,
		groupInterval: 5 * time.Minute,
	}, restartedSender)
	requireNoError(t, writeCompleteTestLog(filepath.Join(logDir, "error-second.log"), 500, "same failure"))
	requireNoError(t, restarted.scan(context.Background(), now.Add(time.Minute)))
	requireNoError(t, writeCompleteTestLog(filepath.Join(logDir, "error-third.log"), 500, "same failure"))
	requireNoError(t, restarted.scan(context.Background(), now.Add(6*time.Minute)))

	// Then
	requireEqual(t, 1, len(firstSender.alerts))
	requireEqual(t, 1, len(restartedSender.alerts))
	requireEqual(t, 3, restartedSender.alerts[0].Occurrences24h)
}

func TestForwarderScan_skipsStaleErrors_whenOccurrenceIsOlderThan24Hours(t *testing.T) {
	// Given
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	requireNoError(t, os.Mkdir(logDir, 0o700))
	statePath := filepath.Join(root, "state", "seen.json")
	sender := &recordingSender{}
	service := newForwarder(forwarderConfig{logDir: logDir, statePath: statePath}, sender)
	now := time.Now()
	requireNoError(t, service.scan(context.Background(), now))
	path := filepath.Join(logDir, "error-stale.log")
	requireNoError(t, writeCompleteTestLog(path, 500, "stale failure"))
	staleTime := now.Add(-25 * time.Hour)
	requireNoError(t, os.Chtimes(path, staleTime, staleTime))

	// When
	requireNoError(t, service.scan(context.Background(), now))

	// Then
	requireEqual(t, 0, sender.calls)
}

func TestForwarderScan_continuesAfterPermanentTelegramError_whenAnotherFileExists(t *testing.T) {
	// Given
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	requireNoError(t, os.Mkdir(logDir, 0o700))
	statePath := filepath.Join(root, "state", "seen.json")
	sender := &recordingSender{err: &telegramAPIError{Status: 400, Description: "Bad Request: message is too long"}}
	service := newForwarder(forwarderConfig{logDir: logDir, statePath: statePath}, sender)
	now := time.Now().Add(time.Second)
	requireNoError(t, service.scan(context.Background(), now))
	requireNoError(t, writeCompleteTestLog(filepath.Join(logDir, "error-first.log"), 400, "first"))
	requireNoError(t, writeCompleteTestLog(filepath.Join(logDir, "error-second.log"), 500, "second"))

	// When
	requireNoError(t, service.scan(context.Background(), now.Add(time.Second)))

	// Then
	requireEqual(t, 2, sender.calls)
}

func TestForwarderScan_retriesConfigurationErrors_whenTelegramAuthorizationRecovers(t *testing.T) {
	// Given
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	requireNoError(t, os.Mkdir(logDir, 0o700))
	statePath := filepath.Join(root, "state", "seen.json")
	sender := &recordingSender{err: &telegramAPIError{Status: 401, Description: "Unauthorized"}}
	service := newForwarder(forwarderConfig{logDir: logDir, statePath: statePath}, sender)
	now := time.Now().Add(time.Second)
	requireNoError(t, service.scan(context.Background(), now))
	requireNoError(t, writeCompleteTestLog(filepath.Join(logDir, "error-retry.log"), 500, "retry me"))

	// When
	if err := service.scan(context.Background(), now.Add(time.Second)); err == nil {
		t.Fatal("expected Telegram authorization error")
	}
	sender.err = nil
	requireNoError(t, service.scan(context.Background(), now.Add(2*time.Second)))

	// Then
	requireEqual(t, 2, sender.calls)
	requireEqual(t, 1, len(sender.alerts))
}

func TestForwarderScan_preservesExistingGroups_whenNewGroupDeliveryIsTransient(t *testing.T) {
	// Given
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	requireNoError(t, os.Mkdir(logDir, 0o700))
	sender := &recordingSender{err: errors.New("temporary Telegram failure")}
	service := newForwarder(forwarderConfig{logDir: logDir, statePath: filepath.Join(root, "state.json")}, sender)
	service.loaded = true
	service.state = newForwarderState()
	now := time.Now()
	for index := 0; index < maxTrackedGroups; index++ {
		group := errorGroup{LastSent: now}.recordOccurrence(now, now)
		service.state.Groups[statusText(index)] = group
	}
	requireNoError(t, writeCompleteTestLog(filepath.Join(logDir, "error-new-group.log"), 500, "new group"))

	// When
	if err := service.scan(context.Background(), now.Add(time.Second)); err == nil {
		t.Fatal("expected transient Telegram error")
	}

	// Then
	requireEqual(t, maxTrackedGroups, len(service.state.Groups))
}

func TestForwarderScan_keepsUnreadableFilePending_whenReadErrorIsTransient(t *testing.T) {
	// Given
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	requireNoError(t, os.Mkdir(logDir, 0o700))
	service := newForwarder(forwarderConfig{logDir: logDir, statePath: filepath.Join(root, "state.json")}, &recordingSender{})
	now := time.Now().Add(time.Second)
	requireNoError(t, service.scan(context.Background(), now))
	filename := "error-transient.log"
	requireNoError(t, os.Symlink(filepath.Join(root, "missing-target"), filepath.Join(logDir, filename)))

	// When
	requireNoError(t, service.scan(context.Background(), now.Add(time.Second)))

	// Then
	if _, seen := service.state.Seen[filename]; seen {
		t.Fatal("transient read error was permanently marked as seen")
	}
}

func writeCompleteTestLog(path string, status int, message string) error {
	content := "URL: /v1/responses\n" +
		"=== RESPONSE ===\n" +
		"Status: " + statusText(status) + "\n" +
		"Content-Type: application/json\n\n" +
		`{"error":{"message":"` + message + `"}}` + "\n"
	return os.WriteFile(path, []byte(content), 0o600)
}
