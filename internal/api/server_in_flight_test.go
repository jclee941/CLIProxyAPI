package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func inFlightCount(t *testing.T, server *Server) int64 {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v0/management/in-flight", nil)
	req.Header.Set("Authorization", "Bearer test-management-key")
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("in-flight status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	var payload struct {
		InFlight *int64 `json:"in_flight"`
	}
	if errUnmarshal := json.Unmarshal(rr.Body.Bytes(), &payload); errUnmarshal != nil || payload.InFlight == nil {
		t.Fatalf("in-flight body = %s, want {\"in_flight\": <count>}", rr.Body.String())
	}
	return *payload.InFlight
}

func blockingRoute(server *Server, path string) (started chan struct{}, release chan struct{}) {
	started = make(chan struct{})
	release = make(chan struct{})
	server.engine.GET(path, func(c *gin.Context) {
		close(started)
		<-release
		c.String(http.StatusOK, "ok")
	})
	return started, release
}

func serveInBackground(server *Server, req *http.Request) <-chan int {
	answered := make(chan int, 1)
	go func() {
		rr := httptest.NewRecorder()
		server.engine.ServeHTTP(rr, req)
		answered <- rr.Code
	}()
	return answered
}

func await[T any](t *testing.T, signal <-chan T, what string) T {
	t.Helper()
	select {
	case value := <-signal:
		return value
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

func TestInFlightCountsAnAPIRequestUntilItAnswers(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "test-management-key")
	server := newTestServer(t)
	started, release := blockingRoute(server, "/v1/test-slow")

	if got := inFlightCount(t, server); got != 0 {
		t.Fatalf("idle in-flight = %d, want 0", got)
	}

	answered := serveInBackground(server, httptest.NewRequest(http.MethodGet, "/v1/test-slow", nil))
	await(t, started, "the slow request to start")
	if got := inFlightCount(t, server); got != 1 {
		t.Fatalf("in-flight while answering = %d, want 1", got)
	}

	close(release)
	if code := await(t, answered, "the slow request to answer"); code != http.StatusOK {
		t.Fatalf("slow request status = %d, want %d", code, http.StatusOK)
	}
	if got := inFlightCount(t, server); got != 0 {
		t.Fatalf("in-flight after the answer = %d, want 0", got)
	}
}

func TestInFlightLeavesOutWebsocketSessions(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "test-management-key")
	server := newTestServer(t)
	started, release := blockingRoute(server, "/v1/test-socket")
	defer close(release)

	req := httptest.NewRequest(http.MethodGet, "/v1/test-socket", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	_ = serveInBackground(server, req)
	await(t, started, "the websocket session to start")

	if got := inFlightCount(t, server); got != 0 {
		t.Fatalf("in-flight with an open websocket session = %d, want 0", got)
	}
}
