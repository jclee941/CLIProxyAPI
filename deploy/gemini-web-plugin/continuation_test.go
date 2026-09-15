package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
)

const testCallerScope = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

type continuationNoNetwork struct{ t *testing.T }

func (transport continuationNoNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	transport.t.Error("prepare must not contact upstream")
	return nil, failure(500, "unexpected_network")
}

func continuationFixture(t *testing.T) (*service, localSession) {
	t.Helper()
	store, err := openSessionStore(filepath.Join(t.TempDir(), "sessions"), sessionKeyFixture())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.close(); err != nil {
			t.Error(err)
		}
	})
	local := localRecordFixture(t)
	local.State = localReady
	if err := store.write(local); err != nil {
		t.Fatal(err)
	}
	service := newService(nil)
	service.sessions = store
	service.config.NativeGeneration = true
	service.config.NativeContinuation = true
	service.client.Transport = continuationNoNetwork{t}
	return service, local
}

func TestContinuationPrepareReturnsReceiptWithoutSubmission(t *testing.T) {
	// Given an encrypted local application session.
	service, local := continuationFixture(t)
	// When a native client prepares a turn before spending quota.
	result := invoke(t, service, "executor.execute", executorRequest{
		AuthID: local.Target.ID, AuthProvider: provider, Model: flashModel,
		Format: "gemini", SourceFormat: "gemini", StorageJSON: jsonFixture(t, local.Target),
		Payload: []byte(`{"geminiWebContinuation":{"action":"prepare"}}`),
	})
	// Then it receives a durable opaque receipt, not a generation.
	if !result.OK {
		t.Fatalf("prepare: %+v", result.Error)
	}
	var response struct{ Payload []byte }
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Continuation struct {
			Token string `json:"token"`
			State string `json:"state"`
		} `json:"geminiWebContinuation"`
	}
	if err := json.Unmarshal(response.Payload, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Continuation.Token) != 64 || body.Continuation.State != "prepared" {
		t.Fatalf("receipt: %+v", body.Continuation)
	}
}
