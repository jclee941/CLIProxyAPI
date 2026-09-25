package pluginhost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func awaitCallbackCancellation(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("callback stream was not canceled")
	}
}

func TestHostHTTPStreamLifetimeTransfersOnlyAfterSuccessfulOpen(t *testing.T) {
	for _, scenario := range []string{"callback close", "explicit close", "bridge unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			// Given a real upstream stream that finishes only when its context is canceled.
			stopped := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(stopped)
				w.Header().Set("Content-Type", "text/event-stream")
				if _, err := w.Write([]byte("first")); err != nil {
					t.Error(err)
					return
				}
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer server.Close()
			parent, cancelParent := context.WithCancel(t.Context())
			defer cancelParent()
			host := New()
			callbackID, closeCallback := host.openCallbackContext(parent)
			defer closeCallback()
			if scenario == "bridge unavailable" {
				host.httpStreams = nil
			}
			raw, err := json.Marshal(rpcHostHTTPRequest{Method: http.MethodGet, URL: server.URL, HostCallbackID: callbackID})
			if err != nil {
				t.Fatal(err)
			}
			response, err := host.callHostHTTPDoStream(parent, raw)
			if scenario == "bridge unavailable" {
				// When ownership cannot transfer, startup must cancel its upstream context.
				if err == nil {
					t.Fatal("missing bridge accepted")
				}
				awaitCallbackCancellation(t, stopped)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			opened, err := decodeRPCEnvelope[rpcHostHTTPStreamResponse](response)
			if err != nil {
				t.Fatal(err)
			}
			defer host.httpStreams.close("", nil, opened.StreamID)
			// Successful callback return must not cancel a stream before its first read.
			chunk, done, err := host.httpStreams.read(parent, "", nil, opened.StreamID)
			if err != nil || done || string(chunk.Payload) != "first" {
				t.Fatalf("first read: %q done=%v err=%v", chunk.Payload, done, err)
			}
			// When the owning callback or an explicit stream close ends its lifetime.
			if scenario == "callback close" {
				closeCallback()
			} else {
				raw, err := json.Marshal(rpcHostHTTPStreamCloseRequest{StreamID: opened.StreamID})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := host.callHostHTTPStreamClose(parent, raw); err != nil {
					t.Fatal(err)
				}
			}
			// Then the bridge releases ownership and the real upstream connection stops.
			host.httpStreams.mu.Lock()
			remaining := len(host.httpStreams.streams)
			host.httpStreams.mu.Unlock()
			if remaining != 0 {
				t.Fatalf("callback retained %d HTTP streams", remaining)
			}
			awaitCallbackCancellation(t, stopped)
		})
	}
}

func TestHostModelStreamLifetimeStartupAndTerminalPaths(t *testing.T) {
	for _, scenario := range []string{"startup error", "bridge unavailable", "nil chunks", "eof", "terminal error", "read canceled"} {
		t.Run(scenario, func(t *testing.T) {
			// Given an executor that exposes the exact context whose ownership transfers.
			host := New()
			var streamCtx context.Context
			chunks := make(chan handlers.ModelExecutionChunk, 1)
			host.SetModelExecutor(&fakeHostModelExecutor{executeModelStream: func(ctx context.Context, _ handlers.ModelExecutionRequest) (handlers.ModelExecutionStream, *interfaces.ErrorMessage) {
				streamCtx = ctx
				if scenario == "startup error" {
					return handlers.ModelExecutionStream{}, &interfaces.ErrorMessage{StatusCode: 502}
				}
				if scenario == "nil chunks" {
					return handlers.ModelExecutionStream{}, nil
				}
				return handlers.ModelExecutionStream{StatusCode: 200, Chunks: chunks}, nil
			}})
			if scenario == "bridge unavailable" {
				host.modelStreams = nil
			}
			raw, err := json.Marshal(pluginapi.HostModelExecutionRequest{Model: "test-model", Stream: true})
			if err != nil {
				t.Fatal(err)
			}
			response, err := host.callHostModelExecuteStream(t.Context(), raw)
			switch scenario {
			case "startup error", "bridge unavailable", "nil chunks":
				// When startup fails, there is no stream owner to clean up later.
				if err == nil {
					t.Fatal("failed startup accepted")
				}
				awaitCallbackCancellation(t, streamCtx.Done())
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			opened, err := decodeRPCEnvelope[pluginapi.HostModelStreamResponse](response)
			if err != nil {
				t.Fatal(err)
			}
			defer host.modelStreams.close(opened.StreamID)
			if streamCtx.Err() != nil {
				t.Fatal("successful callback canceled its live stream")
			}
			// When a terminal read ends the transferred stream lifetime.
			readCtx, cancelRead := context.WithCancel(t.Context())
			defer cancelRead()
			switch scenario {
			case "eof":
				close(chunks)
			case "terminal error":
				chunks <- handlers.ModelExecutionChunk{Err: &handlers.ModelExecutionStreamError{StatusCode: 502, Message: "terminal"}}
			case "read canceled":
				cancelRead()
			}
			raw, err = json.Marshal(pluginapi.HostModelStreamReadRequest{StreamID: opened.StreamID})
			if err != nil {
				t.Fatal(err)
			}
			_, readErr := host.callHostModelStreamRead(readCtx, raw)
			if scenario == "read canceled" && readErr == nil {
				t.Fatal("canceled read succeeded")
			}
			if scenario != "read canceled" && readErr != nil {
				t.Fatal(readErr)
			}
			// Then the execution context is canceled and its bridge entry is removed.
			awaitCallbackCancellation(t, streamCtx.Done())
			if count := hostModelStreamCountForTest(t, host); count != 0 {
				t.Fatalf("retained model streams: %d", count)
			}
		})
	}
}
