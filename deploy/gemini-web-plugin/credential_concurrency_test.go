package main

import (
	"testing"
)

func TestFlashSharesCredentialLease_whenAnotherReaderActive(t *testing.T) {
	service := newService(nil)
	reference := recordFixture(t, "a").TokenRef
	first, err := service.acquireCredential(reference, false)
	if err != nil {
		t.Fatal(err)
	}
	defer first.guard.RUnlock()

	second, err := service.acquireCredential(reference, false)

	if err != nil {
		t.Fatal("Flash readers were serialized")
	}
	second.guard.RUnlock()
}
