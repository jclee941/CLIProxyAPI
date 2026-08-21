package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCPAClient_parsesAPIKeyUsage_andSendsBearerToken(t *testing.T) {
	// Given
	cpa := newCPAStub(t)
	client := newCPAClient(&http.Client{}, cpa.server.URL, testManagementKey, 5*time.Second)

	// When
	usage, errFetch := client.FetchAPIKeyUsage(context.Background())

	// Then
	requireNoError(t, errFetch)
	requireEqual(t, "Bearer "+testManagementKey, cpa.lastAuthHeader())
	requireEqual(t, 2, len(usage))
	entry := usage["codex"]["https://api.openai.com|"+codexKey]
	requireEqual(t, int64(7), entry.Success)
	requireEqual(t, int64(3), entry.Failed)
	requireEqual(t, 1, len(entry.RecentRequests))
	requireEqual(t, "09:00-09:03", entry.RecentRequests[0].Time)
	requireEqual(t, int64(2), entry.RecentRequests[0].Failed)
}

func TestCPAClient_parsesUsageStatisticsStatus(t *testing.T) {
	// Given
	cpa := newCPAStub(t)
	client := newCPAClient(&http.Client{}, cpa.server.URL, testManagementKey, 5*time.Second)

	// When
	status, errFetch := client.FetchStatus(context.Background())

	// Then
	requireNoError(t, errFetch)
	requireEqual(t, true, status.Enabled)
}

func TestCPAClient_returnsTypedError_whenManagementRejectsRequest(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()
	client := newCPAClient(&http.Client{}, server.URL, testManagementKey, 5*time.Second)

	// When
	_, errFetch := client.FetchAPIKeyUsage(context.Background())

	// Then
	var apiErr *cpaAPIError
	if !errors.As(errFetch, &apiErr) {
		t.Fatalf("expected *cpaAPIError, got %v", errFetch)
	}
	requireEqual(t, http.StatusUnauthorized, apiErr.Status)
	requireEqual(t, cpaAPIKeyUsagePath, apiErr.Path)
}

func TestCPAClient_redactsManagementKey_whenTransportFails(t *testing.T) {
	// Given
	client := newCPAClient(&http.Client{Transport: keyEchoTransport{}}, "http://cpa.invalid", testManagementKey, time.Second)

	// When
	_, errFetch := client.FetchStatus(context.Background())

	// Then
	if errFetch == nil {
		t.Fatal("expected transport error")
	}
	if strings.Contains(errFetch.Error(), testManagementKey) {
		t.Fatalf("transport error leaked management key: %v", errFetch)
	}
	requireContains(t, errFetch.Error(), redactedValue)
}

// keyEchoTransport fails with an error containing the management key so the
// redaction boundary can be observed.
type keyEchoTransport struct{}

func (keyEchoTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return nil, errors.New("dial failed with " + request.Header.Get("Authorization"))
}
