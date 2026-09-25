package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func writeAccountFixture(t *testing.T, writer http.ResponseWriter, request *http.Request) {
	t.Helper()
	if request.URL.Path == "/v1/usage" {
		writeFixture(t, writer, `{"tier":"PRO","tier_code":2,"metrics":[{"remaining_units":12,"usage_fraction":0.25,"reset_unix_seconds":1800000000,"window_kind":"5h","unit":"provider_compute_unit"}],"source":"GoogleWeb","estimated":false,"observed_at":1234}`)
		return
	}
	writeFixture(t, writer, `{"available":true,"observed_at":1234,"models":[{"capability_id":"actual","display_name":"3.8 Flash","mode":1}]}`)
}

func listingFixture(t *testing.T, letters []string, handler http.HandlerFunc) (*service, []storageRecord) {
	t.Helper()
	records := make([]storageRecord, 0, len(letters))
	tokens := make(map[string]sessionToken, len(letters))
	for _, letter := range letters {
		record := recordFixture(t, letter)
		records = append(records, record)
		tokens[record.TokenRef] = sessionToken{encodedToken("test-" + letter)}
	}
	service := newService(accountHostScoped(t, records, ""))
	seedSessions(t, service, tokens)
	localSidecar(t, service, handler)
	return service, records
}

func listAccountViews(t *testing.T, service *service, scope string) []accountView {
	t.Helper()
	result := invoke(t, service, "management.handle", managementRequest{Method: "GET", Path: accountsPath, HostCallbackID: scope})
	var response httpResponse
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("accounts listing failed: %s", response.Body)
	}
	var body struct {
		Accounts []accountView `json:"accounts"`
		Provider string        `json:"provider"`
	}
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Provider != provider {
		t.Fatalf("provider=%q", body.Provider)
	}
	return body.Accounts
}

func TestAccountsListInspectsEveryAccountConcurrently_andKeepsEntryOrder(t *testing.T) {
	letters := []string{"a", "b", "c"}
	var mu sync.Mutex
	inflight, peak := 0, 0
	barrier := make(chan struct{})
	var reached sync.Once
	service, records := listingFixture(t, letters, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/usage" {
			mu.Lock()
			inflight++
			if inflight > peak {
				peak = inflight
			}
			complete := inflight == len(letters)
			mu.Unlock()
			if complete {
				reached.Do(func() { close(barrier) })
			}
			select {
			case <-barrier:
			case <-time.After(2 * time.Second):
			}
			mu.Lock()
			inflight--
			mu.Unlock()
		}
		writeAccountFixture(t, writer, request)
	})

	accounts := listAccountViews(t, service, "scope-list")

	if len(accounts) != len(records) {
		t.Fatalf("accounts=%d want %d", len(accounts), len(records))
	}
	for index, record := range records {
		if accounts[index].ID != record.ID {
			t.Fatalf("account %d is %q, want %q: parallel inspection lost entry order", index, accounts[index].ID, record.ID)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if peak != len(letters) {
		t.Fatalf("peaked at %d concurrent inspection(s), want %d", peak, len(letters))
	}
}

func TestAccountsListReusesRecentResults_untilAWriteOrExpiry(t *testing.T) {
	var upstream atomic.Int64
	service, records := listingFixture(t, []string{"a"}, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/usage" {
			upstream.Add(1)
		}
		writeAccountFixture(t, writer, request)
	})
	var clock atomic.Int64
	clock.Store(1700000000)
	service.now = func() time.Time { return time.Unix(clock.Load(), 0) }

	first := listAccountViews(t, service, "call-1")
	second := listAccountViews(t, service, "call-2")

	if upstream.Load() != 1 {
		t.Fatalf("repeated listing queried upstream %d time(s), want 1", upstream.Load())
	}
	if len(first) != 1 || len(second) != 1 || first[0].ID != second[0].ID || second[0].Status != "ready" || second[0].Usage == nil {
		t.Fatalf("reused listing lost content: %+v", second)
	}

	refresh := invoke(t, service, "management.handle", managementRequest{Method: "POST", Path: refreshPath, HostCallbackID: "scope-list", Body: jsonFixture(t, struct {
		ID string `json:"id"`
	}{records[0].ID})})
	if !refresh.OK {
		t.Fatalf("refresh failed: %+v", refresh.Error)
	}
	listAccountViews(t, service, "call-4")
	if upstream.Load() != 3 {
		t.Fatalf("a write left a stale listing cached: upstream=%d want 3", upstream.Load())
	}

	clock.Add(int64(accountsCacheTTL/time.Second) + 1)
	listAccountViews(t, service, "call-5")
	if upstream.Load() != 4 {
		t.Fatalf("expired listing was not refetched: upstream=%d want 4", upstream.Load())
	}
}
