package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDeliveryState_migratesLegacyOccurrences_whenStatePredatesBuckets(t *testing.T) {
	// Given
	now := time.Now().UTC().Truncate(time.Second)
	path := filepath.Join(t.TempDir(), "delivered.json")
	legacy := `{"files":["error-one.log"],"groups":{"fingerprint":{"occurrences":["` + now.Add(-time.Hour).Format(time.RFC3339) + `"],"last_sent":"` + now.Add(-time.Hour).Format(time.RFC3339) + `"}}}`
	requireNoError(t, os.WriteFile(path, []byte(legacy), 0o600))

	// When
	state, found, err := loadDeliveryState(path)

	// Then
	requireNoError(t, err)
	requireEqual(t, true, found)
	requireEqual(t, 1, state.Groups["fingerprint"].occurrenceCount())
	requireEqual(t, 0, len(state.Groups["fingerprint"].LegacyOccurrences))
}

func TestLoadDeliveryState_trimsGroups_whenLegacyStateExceedsCapacity(t *testing.T) {
	// Given
	now := time.Now()
	groups := make(map[string]errorGroup, maxTrackedGroups+10)
	for index := 0; index < maxTrackedGroups+10; index++ {
		groups[statusText(index)] = errorGroup{LastSent: now.Add(time.Duration(index) * time.Second)}.recordOccurrence(now, now)
	}
	data, errMarshal := json.Marshal(deliveryState{Groups: groups})
	requireNoError(t, errMarshal)
	path := filepath.Join(t.TempDir(), "delivered.json")
	requireNoError(t, os.WriteFile(path, data, 0o600))

	// When
	state, _, errLoad := loadDeliveryState(path)

	// Then
	requireNoError(t, errLoad)
	requireEqual(t, maxTrackedGroups, len(state.Groups))
}

func TestForwarderState_makeRoomForGroup_boundsTrackedFingerprints_whenAtCapacity(t *testing.T) {
	// Given
	state := newForwarderState()
	for index := 0; index < maxTrackedGroups; index++ {
		fingerprint := statusText(index)
		state.Groups[fingerprint] = errorGroup{LastSent: time.Unix(int64(index+1), 0)}
	}

	// When
	state.makeRoomForGroup()

	// Then
	requireEqual(t, maxTrackedGroups-1, len(state.Groups))
	if _, exists := state.Groups["0"]; exists {
		t.Fatal("oldest group was not evicted")
	}
}
