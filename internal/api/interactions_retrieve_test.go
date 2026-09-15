package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInteractionsRetrievalRouteRegistered(t *testing.T) {
	// Given the real authenticated server route table.
	server := newTestServer(t)
	// When an unauthenticated caller requests a receipt.
	request := httptest.NewRequest(http.MethodGet, "/v1beta/interactions/receipt", nil)
	response := httptest.NewRecorder()
	server.engine.ServeHTTP(response, request)
	// Then the registered route enforces authentication, rather than returning router 404.
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("GET status = %d, want 401; body=%s", response.Code, response.Body.String())
	}
}
