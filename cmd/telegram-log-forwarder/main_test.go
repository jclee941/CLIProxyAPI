package main

import (
	"testing"
	"time"
)

func TestValidateIntervals_rejectsUnsafeValues_whenTickerWouldPanicOrGroupingWouldDisable(t *testing.T) {
	// Given / When
	errPoll := validateIntervals(0, time.Second, time.Minute)
	errSettle := validateIntervals(time.Second, -time.Second, time.Minute)
	errGroup := validateIntervals(time.Second, time.Second, -time.Minute)

	// Then
	if errPoll == nil || errSettle == nil || errGroup == nil {
		t.Fatalf("expected invalid intervals to be rejected: poll=%v settle=%v group=%v", errPoll, errSettle, errGroup)
	}
}
