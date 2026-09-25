package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// Pause the actual first upstream request, before credential renewal and its
// host.auth callbacks. Releasing this after POST returns also exercises refresh
// with the old callback ID, not just generation after a completed refresh.
type backgroundGateTransport struct {
	base    http.RoundTripper
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (gate *backgroundGateTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	gate.once.Do(func() { close(gate.entered) })
	select {
	case <-gate.release:
		return gate.base.RoundTrip(request)
	case <-request.Context().Done():
		return nil, request.Context().Err()
	}
}

func backgroundFixture(t *testing.T, fixture *continuationWebFixture) (*service, localSession, <-chan struct{}, func()) {
	t.Helper()
	service, local := continuationFixture(t)
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	release := make(chan struct{})
	gate := &backgroundGateTransport{base: service.client.Transport, entered: make(chan struct{}), release: release}
	service.client.Transport = gate
	finish := sync.OnceFunc(func() { close(release) })
	t.Cleanup(func() {
		finish()
		if err := service.shutdownSessions(); err != nil {
			t.Error(err)
		}
	})
	return service, local, gate.entered, finish
}

func backgroundCreate(t *testing.T, ctx context.Context, service *service, request executorRequest) continuationResult {
	t.Helper()
	raw := jsonFixture(t, request)
	returned := make(chan []byte, 1)
	go func() { returned <- service.handle(ctx, "executor.execute", raw) }()
	var result envelope
	if err := json.Unmarshal(interactionAwait(t, returned), &result); err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("background create failed: %+v", result.Error)
	}
	var response continuationResult
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func backgroundOperation(t *testing.T, service *service, created continuationResult) (string, *interactionOperation) {
	t.Helper()
	var body struct {
		ID, Object, Status string
		Steps              []json.RawMessage
	}
	if err := json.Unmarshal(created.Payload, &body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "interaction" || body.Status != "in_progress" || len(body.ID) != 64 || len(body.Steps) != 0 {
		t.Fatalf("background acknowledgement: %s", created.Payload)
	}
	service.interactionsMu.Lock()
	operation := service.interactions[body.ID]
	service.interactionsMu.Unlock()
	if operation == nil {
		t.Fatal("background work is not lifecycle-owned while upstream is blocked")
	}
	return body.ID, operation
}

func backgroundRestart(t *testing.T, previous *service) *service {
	t.Helper()
	path := previous.sessions.directory.Name()
	if err := previous.shutdownSessions(); err != nil {
		t.Fatal(err)
	}
	store, err := openSessionStore(path, sessionKeyFixture())
	if err != nil {
		t.Fatal(err)
	}
	service := newService(nil)
	service.sessions = store
	service.config.NativeGeneration, service.config.NativeContinuation = true, true
	service.client.Transport = continuationNoNetwork{t}
	t.Cleanup(func() {
		if err := service.shutdownSessions(); err != nil {
			t.Error(err)
		}
	})
	return service
}

func TestBackgroundCreateReturnsDurableIDAndSurvivesCallerCancellationAndRestart(t *testing.T) {
	for _, option := range []string{"", `,"store":true`} {
		t.Run("store"+option, func(t *testing.T) {
			// Given real upstream HTTP and encrypted storage, with refresh blocked.
			fixture := &continuationWebFixture{video: true, rotate: true}
			service, local, entered, release := backgroundFixture(t, fixture)
			request := interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","input":"first","background":true`+option+`}`)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			// When the unary create returns before even the first upstream call ends.
			created := backgroundCreate(t, ctx, service, request)
			id, operation := backgroundOperation(t, service, created)
			interactionAwait(t, entered)
			stored, err := service.sessions.read(local.Target.TokenRef)
			if err != nil {
				t.Fatal(err)
			}
			turns, err := continuationTurns(stored)
			if err != nil || turns[continuationKey(id)].CallerScope != testCallerScope {
				t.Fatalf("acknowledged receipt is not durable/caller-bound: %v", err)
			}
			get := interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","id":"`+id+`"}`)
			get.Alt = interactionRetrieveAlt
			pending, err := service.executeInteraction(t.Context(), get)
			if err != nil || !bytes.Equal(pending.(continuationResult).Payload, created.Payload) {
				t.Fatalf("GET before completion: %v", err)
			}
			other := get
			other.Metadata.CallerScope = strings.Repeat("d", 64)
			if _, err := service.executeInteraction(t.Context(), other); safeCredentialCode(err) != "interaction_not_found" {
				t.Fatalf("cross-caller pending retrieval: %v", err)
			}
			cancel()
			release()
			interactionAwait(t, operation.done)
			// Then work, including cookie rotation and host auth save, completes.
			if operation.err != nil {
				t.Fatal(operation.err)
			}
			stored, err = service.sessions.read(local.Target.TokenRef)
			if err != nil || stored.Target.SessionRevision != 2 {
				t.Fatalf("detached credential refresh: revision=%d err=%v", stored.Target.SessionRevision, err)
			}
			before := interactionID(t, invoke(t, service, "executor.execute", get))
			restarted := backgroundRestart(t, service)
			if after := interactionID(t, invoke(t, restarted, "executor.execute", get)); before != id || after != id {
				t.Fatalf("restart changed the receipt: %q / %q / %q", id, before, after)
			}
			if _, err := restarted.executeInteraction(t.Context(), other); safeCredentialCode(err) != "interaction_not_found" {
				t.Fatalf("cross-caller stored retrieval: %v", err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if len(fixture.fields) != 1 {
				t.Fatalf("background/restart submitted %d turns", len(fixture.fields))
			}
		})
	}
}

func TestBackgroundFailureIsTerminalAndDurableWithoutUpstreamRecovery(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		fixture *continuationWebFixture
		code    string
		count   int
	}{
		{name: "declined", fixture: &continuationWebFixture{replyOnly: true}, code: "no_video_generated", count: 1},
		{name: "no-video", fixture: &continuationWebFixture{}, code: "no_video_generated", count: 1},
		{name: "expired", fixture: &continuationWebFixture{expired: true}, code: "auth_error"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service, local, entered, release := backgroundFixture(t, scenario.fixture)
			request := interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","input":"first","background":true}`)
			created := backgroundCreate(t, t.Context(), service, request)
			id, operation := backgroundOperation(t, service, created)
			interactionAwait(t, entered)
			release()
			interactionAwait(t, operation.done)
			get := interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","id":"`+id+`"}`)
			get.Alt = interactionRetrieveAlt
			service.client.Transport = continuationNoNetwork{t}
			result, err := service.executeInteraction(t.Context(), get)
			if err != nil {
				t.Fatalf("accepted background failure escaped GET instead of terminal status: %v", err)
			}
			payload := result.(continuationResult).Payload
			var body struct {
				ID, Status string
				Steps      []json.RawMessage
				Error      struct{ Code, Message string }
			}
			if err := json.Unmarshal(payload, &body); err != nil {
				t.Fatal(err)
			}
			if body.ID != id || body.Status != "failed" || len(body.Steps) != 0 || body.Error.Code != scenario.code || body.Error.Message == "" {
				t.Fatalf("terminal failure: %s", payload)
			}
			restarted := backgroundRestart(t, service)
			replay, err := restarted.executeInteraction(t.Context(), get)
			if err != nil || !bytes.Equal(payload, replay.(continuationResult).Payload) {
				t.Fatalf("failed receipt changed across restart: %v", err)
			}
			scenario.fixture.mu.Lock()
			defer scenario.fixture.mu.Unlock()
			if len(scenario.fixture.fields) != scenario.count {
				t.Fatalf("failed GET resubmitted: %d", len(scenario.fixture.fields))
			}
		})
	}
}

func TestBackgroundStoreFalseIsRejectedBeforeReceiptOrNetwork(t *testing.T) {
	// https://ai.google.dev/gemini-api/docs/interactions#data-storage-and-retention
	// Storage defaults to true; opting out is incompatible with background work.
	service, local := continuationFixture(t)
	for _, stream := range []bool{false, true} {
		body := jsonFixture(t, map[string]any{"model": interactionOmniModel, "input": "first", "background": true, "store": false, "stream": stream})
		result := interactionCall(t, service, local, string(body))
		if result.OK || result.Error.HTTPStatus != http.StatusBadRequest || result.Error.Code != "gemini_web_omni:interaction_background_requires_store" {
			t.Fatalf("background/store=false contract: %+v", result.Error)
		}
	}
	stored, err := service.sessions.read(local.Target.TokenRef)
	if err != nil || stored.Continuations != "" {
		t.Fatalf("invalid create persisted a receipt: %v", err)
	}
}
