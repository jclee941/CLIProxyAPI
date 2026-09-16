package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInteractionCompletedGETReplaysEncryptedResultAfterRestartWithoutNetwork(t *testing.T) {
	// Given one completed video persisted beside the encrypted receipt.
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	id := interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`))
	request := interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","id":"`+id+`"}`)
	request.Alt = interactionRetrieveAlt
	before, err := service.executeInteraction(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	path := service.sessions.directory.Name()
	encrypted, err := os.ReadFile(filepath.Join(path, "interaction-"+continuationKey(id)+".bin"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte("video/mp4")) || bytes.Contains(encrypted, []byte("MDAwMGZ0eXB2aWRlbw==")) {
		t.Fatal("result stored in plaintext")
	}
	if err := service.sessions.close(); err != nil {
		t.Fatal(err)
	}
	store, err := openSessionStore(path, sessionKeyFixture())
	if err != nil {
		t.Fatal(err)
	}
	service.sessions = store
	t.Cleanup(func() {
		if err := store.close(); err != nil {
			t.Error(err)
		}
	})
	service.client.Transport = continuationNoNetwork{t}
	// When GET runs after process/store recovery with upstream unreachable.
	after, err := service.executeInteraction(t.Context(), request)
	// Then byte-identical completed output comes from authenticated ciphertext.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.(continuationResult).Payload, after.(continuationResult).Payload) {
		t.Fatal("completed replay changed")
	}
	request.Metadata.CallerScope = strings.Repeat("d", 64)
	if _, err := service.executeInteraction(t.Context(), request); err == nil {
		t.Fatal("other caller read cached result")
	}
}

func TestInteractionGETPreparedReceiptNeverSubmits(t *testing.T) {
	// Given a durable prepared receipt whose POST did not submit before restart.
	service, local := continuationFixture(t)
	request := interactionExecutorRequest(t, local, `{"geminiWebContinuation":{"action":"prepare"}}`)
	request.Model, request.Format, request.SourceFormat = omniModel, "gemini", "gemini"
	result, err := service.executeContinuation(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		View continuationView `json:"geminiWebContinuation"`
	}
	if err := json.Unmarshal(result.(continuationResult).Payload, &receipt); err != nil {
		t.Fatal(err)
	}
	get := interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","id":"`+receipt.View.Token+`"}`)
	get.Alt = interactionRetrieveAlt
	// When GET reads it, the no-network transport forbids any generation or lookup.
	response, err := service.executeInteraction(t.Context(), get)
	// Then it remains unsubmitted and explicitly in progress.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(response.(continuationResult).Payload, []byte(`"in_progress"`)) {
		t.Fatalf("snapshot: %s", response.(continuationResult).Payload)
	}
}

func TestInteractionGETRecoversInterruptedVideoWithoutResubmit(t *testing.T) {
	// Given an upstream transport interrupted after the chat handles were persisted.
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true, interrupted: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	response := interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`)
	if !response.OK {
		t.Fatal(response.Error)
	}
	var encoded struct{ Payload []byte }
	if err := json.Unmarshal(response.Result, &encoded); err != nil {
		t.Fatal(err)
	}
	var pending struct{ ID, Status string }
	if err := json.Unmarshal(encoded.Payload, &pending); err != nil {
		t.Fatal(err)
	}
	if pending.Status != "in_progress" {
		t.Fatalf("pending: %s", encoded.Payload)
	}
	get := interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","id":"`+pending.ID+`"}`)
	get.Alt = interactionRetrieveAlt
	// When the public retrieval operation observes the existing candidate.
	result, err := service.executeInteraction(t.Context(), get)
	// Then recovery completes the same receipt without a second StreamGenerate.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(result.(continuationResult).Payload, []byte(`"completed"`)) {
		t.Fatal("GET did not recover")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 {
		t.Fatal("GET resubmitted generation")
	}
}

func TestInteractionCursorIsReceiptBoundAndCanonical(t *testing.T) {
	// Given the six-event stable stream contract.
	token := strings.Repeat("a", 64)
	for _, cursor := range []string{"other:1", token + ":0", token + ":7", token + ":01", token + ":-1", token + ":x"} {
		// When a caller supplies a foreign or impossible cursor.
		if _, err := interactionCursor(token, cursor); err == nil {
			t.Fatalf("accepted %q", cursor)
		}
	}
	// Then all valid emitted IDs, and the empty initial cursor, are accepted.
	for n, cursor := range []string{"", token + ":1", token + ":2", token + ":3", token + ":4", token + ":5", token + ":6"} {
		if got, err := interactionCursor(token, cursor); err != nil || got != n {
			t.Fatalf("cursor %q: %d %v", cursor, got, err)
		}
	}
}
