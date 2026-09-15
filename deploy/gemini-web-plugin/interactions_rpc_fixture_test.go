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
	"testing"
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
