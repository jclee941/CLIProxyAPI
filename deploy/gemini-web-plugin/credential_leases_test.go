package main

import (
	"errors"
	"testing"
)

func TestCredentialLeaseExcludesWriters_whenReaderActive(t *testing.T) {
	service := newService(nil)
	reference := recordFixture(t, "a").TokenRef
	reader, err := service.acquireCredential(reference, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.guard.RUnlock()

	_, err = service.acquireCredential(reference, true)

	var public *publicError
	if !errors.As(err, &public) || public.Code != "session_busy" {
		t.Fatalf("writer entered shared lease: %v", err)
	}
}
