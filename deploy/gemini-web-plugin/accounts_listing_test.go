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
	if !serveAccountRPC(t, writer, request) {
		t.Errorf("unexpected request %s", request.URL)
	}
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
	localWeb(t, service, handler)
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
		if nativeRPCID(request) == accountCapabilityRPC {
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
		if nativeRPCID(request) == accountCapabilityRPC {
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

	// A card's refresh reads that account live and updates the cached listing.
	refresh := invoke(t, service, "management.handle", managementRequest{Method: "POST", Path: refreshPath, HostCallbackID: "scope-list", Body: jsonFixture(t, struct {
		ID string `json:"id"`
	}{records[0].ID})})
	if !refresh.OK {
		t.Fatalf("refresh failed: %+v", refresh.Error)
	}
	listAccountViews(t, service, "call-3")
	if upstream.Load() != 2 {
		t.Fatalf("a refresh emptied the listing: upstream=%d want 2", upstream.Load())
	}

	// A write route empties it, even one that refused the write.
	invoke(t, service, "management.handle", managementRequest{Method: "POST", Path: resolvePath, HostCallbackID: "scope-list", Body: []byte(`{}`)})
	listAccountViews(t, service, "call-4")
	if upstream.Load() != 3 {
		t.Fatalf("a write left a stale listing cached: upstream=%d want 3", upstream.Load())
	}

	// An expired listing answers at once and is refreshed behind the read.
	var queued []func()
	service.background = func(run func()) { queued = append(queued, run) }
	clock.Add(int64(accountsCacheTTL/time.Second) + 1)
	stale := listAccountViews(t, service, "call-5")
	if upstream.Load() != 3 || len(stale) != 1 || len(queued) != 1 {
		t.Fatalf("expired listing: upstream=%d, %d views, %d refreshes queued", upstream.Load(), len(stale), len(queued))
	}
	queued[0]()
	listAccountViews(t, service, "call-6")
	if upstream.Load() != 4 || len(queued) != 1 {
		t.Fatalf("expired listing was not refreshed once: upstream=%d, %d refreshes", upstream.Load(), len(queued))
	}
}

func TestAccountsListSurvivesFlowSessionExchanges(t *testing.T) {
	// Given: a cached listing.
	var upstream atomic.Int64
	service, _ := listingFixture(t, []string{"a"}, func(writer http.ResponseWriter, request *http.Request) {
		if nativeRPCID(request) == accountCapabilityRPC {
			upstream.Add(1)
		}
		writeAccountFixture(t, writer, request)
	})
	listAccountViews(t, service, "call-1")

	// When: Flow asks for a session exchange and cancels one, as it does many
	// times a minute.
	invoke(t, service, "management.handle", managementRequest{Method: "POST", Path: sessionExchangePath, HostCallbackID: "scope-list", Body: []byte(`{}`)})
	invoke(t, service, "management.handle", managementRequest{Method: "POST", Path: sessionExchangeCancelPath, HostCallbackID: "scope-list", Body: []byte(`{}`)})
	listAccountViews(t, service, "call-2")

	// Then: the listing was still answered from the cache.
	if upstream.Load() != 1 {
		t.Fatalf("session exchanges emptied the listing: upstream=%d want 1", upstream.Load())
	}
}

func TestAccountsListSharesOneRefreshAcrossExpiredReads(t *testing.T) {
	// Given: an expired listing.
	var upstream atomic.Int64
	service, _ := listingFixture(t, []string{"a", "b"}, func(writer http.ResponseWriter, request *http.Request) {
		if nativeRPCID(request) == accountCapabilityRPC {
			upstream.Add(1)
		}
		writeAccountFixture(t, writer, request)
	})
	var clock atomic.Int64
	clock.Store(1700000000)
	service.now = func() time.Time { return time.Unix(clock.Load(), 0) }
	listAccountViews(t, service, "call-1")
	var queued []func()
	service.background = func(run func()) { queued = append(queued, run) }
	clock.Add(int64(accountsCacheTTL/time.Second) + 1)

	// When: two reads arrive before the refresh has run.
	listAccountViews(t, service, "call-2")
	listAccountViews(t, service, "call-3")

	// Then: they share one refresh, which inspects each account once.
	if len(queued) != 1 {
		t.Fatalf("expired reads queued %d refreshes, want 1", len(queued))
	}
	queued[0]()
	if upstream.Load() != 4 {
		t.Fatalf("the shared refresh queried upstream %d time(s) in all, want 4", upstream.Load())
	}
}

func cachedListing(t *testing.T, service *service) []accountView {
	t.Helper()
	service.accountsMu.Lock()
	defer service.accountsMu.Unlock()
	if len(service.accountsCache) != 1 {
		t.Fatalf("%d cached listings, want 1", len(service.accountsCache))
	}
	for _, cached := range service.accountsCache {
		return cached.views
	}
	return nil
}

func TestAccountsListReadAfterAWriteStartsItsOwnRefresh(t *testing.T) {
	// Given: an expired listing whose refresh is held open.
	service, _ := listingFixture(t, []string{"a", "b"}, func(writer http.ResponseWriter, request *http.Request) {
		writeAccountFixture(t, writer, request)
	})
	var clock atomic.Int64
	clock.Store(1700000000)
	service.now = func() time.Time { return time.Unix(clock.Load(), 0) }
	listAccountViews(t, service, "call-1")
	queued := make(chan func(), 4)
	service.background = func(run func()) { queued <- run }
	clock.Add(int64(accountsCacheTTL/time.Second) + 1)
	listAccountViews(t, service, "call-2")
	older := <-queued

	// When: a write empties the cache and a read follows it.
	invoke(t, service, "management.handle", managementRequest{Method: "POST", Path: resolvePath, HostCallbackID: "scope-list", Body: []byte(`{}`)})
	read := make(chan []accountView, 1)
	go func() { read <- listAccountViews(t, service, "call-3") }()
	var newer func()
	select {
	case newer = <-queued:
	case <-time.After(5 * time.Second):
		t.Fatal("the read after the write joined the refresh the write overtook")
	}
	newer()
	views := <-read
	kept := cachedListing(t, service)
	older()

	// Then: the read got its own refresh, and the overtaken one finishing
	// later did not replace that listing in the cache.
	if len(views) != 2 || &cachedListing(t, service)[0] != &kept[0] {
		t.Fatalf("read %d views; the overtaken refresh replaced the cache: %v", len(views), &cachedListing(t, service)[0] != &kept[0])
	}
}

func TestAccountsListKeepsACardRefreshOverAnOlderListing(t *testing.T) {
	// Given: an expired listing whose refresh is held open.
	service, records := listingFixture(t, []string{"a", "b"}, func(writer http.ResponseWriter, request *http.Request) {
		writeAccountFixture(t, writer, request)
	})
	var clock atomic.Int64
	clock.Store(1700000000)
	service.now = func() time.Time { return time.Unix(clock.Load(), 0) }
	listAccountViews(t, service, "call-1")
	var queued []func()
	service.background = func(run func()) { queued = append(queued, run) }
	clock.Add(int64(accountsCacheTTL/time.Second) + 1)
	listAccountViews(t, service, "call-2")

	// When: a card is refreshed, then the older refresh finishes.
	refresh := invoke(t, service, "management.handle", managementRequest{Method: "POST", Path: refreshPath, HostCallbackID: "scope-list", Body: jsonFixture(t, struct {
		ID string `json:"id"`
	}{records[0].ID})})
	carded := cachedListing(t, service)
	queued[0]()

	// Then: the cache still holds the listing with the refreshed card.
	if !refresh.OK || len(queued) != 1 || &cachedListing(t, service)[0] != &carded[0] {
		t.Fatalf("refresh %+v, %d refreshes; the older listing replaced the card: %v", refresh.Error, len(queued), &cachedListing(t, service)[0] != &carded[0])
	}
}
