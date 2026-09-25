package main

import (
	"strings"
	"testing"
)

func TestContinuationStorageCannotExceedSessionReaderLimit(t *testing.T) {
	service, local := continuationFixture(t)
	oversized := local
	oversized.Continuations = strings.Repeat("x", 128*1024)
	// When an expanded encrypted record exceeds the store's bounded reader.
	err := service.sessions.write(oversized)
	// Then the prior durable session survives instead of becoming unreadable.
	if safeCredentialCode(err) != "session_record_too_large" {
		t.Fatalf("oversized write: %v", err)
	}
	stored, err := service.sessions.read(local.Target.TokenRef)
	if err != nil || stored != local {
		t.Fatalf("prior session damaged: %v", err)
	}
}
