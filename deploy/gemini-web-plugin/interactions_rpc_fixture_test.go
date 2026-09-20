package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Test-only cross-module RPC transport: the host's real RPC adapter and stream
// bridge talk to the actual plugin service, encrypted store and upstream HTTP
// fixture. This is never linked into the production shared library.
func TestInteractionRPCFixtureProcess(t *testing.T) {
	callback := os.Getenv("CPA_OMNI_FIXTURE_CALLBACK")
	if callback == "" {
		return
	}
	service, local := continuationFixture(t)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	fixture := &continuationWebFixture{video: true, beforeSubmit: func() { <-release }}
	switch os.Getenv("CPA_OMNI_FIXTURE_RETRIEVAL") {
	case "pending":
		fixture.interrupted, fixture.pending = true, true
		// Advance only on the recovery wait signal, never on elapsed wall time.
		var elapsed atomic.Int64
		now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
		service.now = func() time.Time { return now.Add(time.Duration(elapsed.Load())) }
		service.continuationWait = func(context.Context) error {
			elapsed.Add(int64(webVideoBudget))
			return nil
		}
	case "mismatch":
		fixture.interrupted, fixture.wrongReply = true, true
	case "ended":
		fixture.interrupted = true
	case "unauthorized":
		fixture.interrupted = true
		fixture.beforeSubmit = func() {
			<-release
			// The submit handler holds fixture.mu; later identity probes get 401.
			fixture.expired = true
		}
	}
	continuationWeb(t, service, fixture)
	host := &loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}
	service.host = func(method string, raw []byte) ([]byte, error) {
		if !strings.HasPrefix(method, "host.stream.") {
			return host.call(method, raw)
		}
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, callback+"/"+method, bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(resp.Body)
		if closeErr := resp.Body.Close(); closeErr != nil {
			return nil, closeErr
		}
		return data, readErr
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			auth, err := authFromRecord(local.Target)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			if err := json.NewEncoder(w).Encode(auth); err != nil {
				t.Error(err)
			}
		case "/release":
			releaseOnce.Do(func() { close(release) })
			w.WriteHeader(204)
		case "/end":
			stored, err := service.localStore().read(local.Target.TokenRef)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			turns, err := continuationTurns(stored)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			for key, turn := range turns {
				turn.State, turn.ResultStored = "no_operation", false
				turns[key] = turn
			}
			stored.State, stored.ContinuationActive = localReady, ""
			if err := service.saveContinuations(stored, turns); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			fixture.mu.Lock()
			fixture.expired = true
			fixture.mu.Unlock()
			w.WriteHeader(204)
		case "/stats":
			fixture.mu.Lock()
			fields := append([][]any(nil), fixture.fields...)
			fixture.mu.Unlock()
			if err := json.NewEncoder(w).Encode(fields); err != nil {
				t.Error(err)
			}
		default:
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			result := service.handle(r.Context(), strings.TrimPrefix(r.URL.Path, "/"), raw)
			if _, err := w.Write(result); err != nil {
				t.Error(err)
			}
		}
	}))
	defer server.Close()
	fmt.Println("CPA_OMNI_FIXTURE_READY " + server.URL)
	// EOF is the exact parent teardown signal, not a fixed lifetime or sleep.
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Error(err)
	}
	releaseOnce.Do(func() { close(release) })
	if err := service.shutdownSessions(); err != nil {
		t.Error(err)
	}
}
