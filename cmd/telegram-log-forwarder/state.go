package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const maxTrackedGroups = 256

type deliveryState struct {
	Files  []string              `json:"files"`
	Groups map[string]errorGroup `json:"groups,omitempty"`
}

type occurrenceBucket struct {
	Minute time.Time `json:"minute"`
	Count  int       `json:"count"`
}

type errorGroup struct {
	Buckets           []occurrenceBucket `json:"buckets,omitempty"`
	LegacyOccurrences []time.Time        `json:"occurrences,omitempty"`
	LastSent          time.Time          `json:"last_sent"`
}

type forwarderState struct {
	Seen   map[string]struct{}
	Groups map[string]errorGroup
}

func loadDeliveryState(path string) (forwarderState, bool, error) {
	data, errRead := os.ReadFile(path)
	if errors.Is(errRead, os.ErrNotExist) {
		return newForwarderState(), false, nil
	}
	if errRead != nil {
		return forwarderState{}, false, fmt.Errorf("read delivery state: %w", errRead)
	}
	var state deliveryState
	if errUnmarshal := json.Unmarshal(data, &state); errUnmarshal != nil {
		return forwarderState{}, false, fmt.Errorf("parse delivery state: %w", errUnmarshal)
	}
	seen := make(map[string]struct{}, len(state.Files))
	for _, name := range state.Files {
		seen[name] = struct{}{}
	}
	if state.Groups == nil {
		state.Groups = make(map[string]errorGroup)
	}
	now := time.Now()
	for fingerprint, group := range state.Groups {
		for _, occurrence := range group.LegacyOccurrences {
			group = group.recordOccurrence(occurrence, now)
		}
		group.LegacyOccurrences = nil
		state.Groups[fingerprint] = group.pruned(now)
	}
	loadedState := forwarderState{Seen: seen, Groups: state.Groups}
	loadedState.trimGroups(maxTrackedGroups)
	return loadedState, true, nil
}

func saveDeliveryState(path string, state forwarderState) error {
	files := make([]string, 0, len(state.Seen))
	for name := range state.Seen {
		files = append(files, name)
	}
	sort.Strings(files)
	data, errMarshal := json.Marshal(deliveryState{Files: files, Groups: state.Groups})
	if errMarshal != nil {
		return fmt.Errorf("encode delivery state: %w", errMarshal)
	}
	if errMkdir := os.MkdirAll(filepath.Dir(path), 0o700); errMkdir != nil {
		return fmt.Errorf("create state directory: %w", errMkdir)
	}
	tempPath := path + ".tmp"
	if errWrite := os.WriteFile(tempPath, data, 0o600); errWrite != nil {
		return fmt.Errorf("write delivery state: %w", errWrite)
	}
	if errRename := os.Rename(tempPath, path); errRename != nil {
		return fmt.Errorf("replace delivery state: %w", errRename)
	}
	return nil
}

func newForwarderState() forwarderState {
	return forwarderState{
		Seen:   make(map[string]struct{}),
		Groups: make(map[string]errorGroup),
	}
}

func (g errorGroup) recordOccurrence(occurredAt, now time.Time) errorGroup {
	g = g.pruned(now)
	if occurredAt.IsZero() {
		occurredAt = now
	}
	if occurredAt.Before(now.Add(-24 * time.Hour)) {
		return g
	}
	minute := occurredAt.UTC().Truncate(time.Minute)
	for index := range g.Buckets {
		if g.Buckets[index].Minute.Equal(minute) {
			g.Buckets[index].Count++
			return g
		}
	}
	g.Buckets = append(g.Buckets, occurrenceBucket{Minute: minute, Count: 1})
	sort.Slice(g.Buckets, func(left, right int) bool {
		return g.Buckets[left].Minute.Before(g.Buckets[right].Minute)
	})
	return g
}

func (g errorGroup) pruned(now time.Time) errorGroup {
	cutoff := now.Add(-24 * time.Hour).UTC().Truncate(time.Minute)
	kept := make([]occurrenceBucket, 0, len(g.Buckets))
	for _, bucket := range g.Buckets {
		if !bucket.Minute.Before(cutoff) {
			kept = append(kept, bucket)
		}
	}
	g.Buckets = kept
	return g
}

func (g errorGroup) occurrenceCount() int {
	count := 0
	for _, bucket := range g.Buckets {
		count += bucket.Count
	}
	return count
}

func (s *forwarderState) pruneGroups(now time.Time) bool {
	changed := false
	for fingerprint, group := range s.Groups {
		before := len(group.Buckets)
		group = group.pruned(now)
		if group.occurrenceCount() == 0 {
			delete(s.Groups, fingerprint)
			changed = true
			continue
		}
		if len(group.Buckets) != before {
			s.Groups[fingerprint] = group
			changed = true
		}
	}
	return changed
}

func (s *forwarderState) makeRoomForGroup() {
	s.trimGroups(maxTrackedGroups - 1)
}

func (s *forwarderState) trimGroups(limit int) {
	for len(s.Groups) > limit {
		s.deleteOldestGroup()
	}
}

func (s *forwarderState) deleteOldestGroup() {
	oldestKey := ""
	oldestTime := time.Now().Add(100 * 365 * 24 * time.Hour)
	for fingerprint, group := range s.Groups {
		activity := group.LastSent
		if len(group.Buckets) > 0 && group.Buckets[len(group.Buckets)-1].Minute.After(activity) {
			activity = group.Buckets[len(group.Buckets)-1].Minute
		}
		if activity.Before(oldestTime) {
			oldestKey = fingerprint
			oldestTime = activity
		}
	}
	delete(s.Groups, oldestKey)
}

func alertFingerprint(alert errorAlert) string {
	payload := fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s", alert.Status, alert.API, alert.Model, alert.Route, alert.Message)
	sum := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("%x", sum[:12])
}
