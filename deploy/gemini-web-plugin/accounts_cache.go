package main

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const accountsCacheTTL = 60 * time.Second

func accountsScope(entries []hostEntry) string {
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		keys = append(keys, entry.ID+":"+strconv.FormatBool(entry.Disabled))
	}
	sort.Strings(keys)
	return strings.Join(keys, "\n")
}

type cachedAccountList struct {
	views     []accountView
	expiresAt time.Time
}

// cachedAccounts returns the scope's last listing and whether it is still
// inside its freshness window. Listing one account costs two upstream round
// trips to Google, so a dashboard read answers from the last listing and an
// expired one is refreshed behind it.
func (service *service) cachedAccounts(scope string) ([]accountView, bool, bool) {
	service.accountsMu.Lock()
	defer service.accountsMu.Unlock()
	cached, exists := service.accountsCache[scope]
	if !exists {
		return nil, false, false
	}
	return cached.views, service.now().Before(cached.expiresAt), true
}

// storeAccounts keeps a listing unless an account write emptied the cache
// after its inspection began, which would make it stale on arrival.
func (service *service) storeAccounts(scope string, views []accountView, epoch uint64) {
	service.accountsMu.Lock()
	defer service.accountsMu.Unlock()
	if epoch != service.accountsEpoch {
		return
	}
	if service.accountsCache == nil {
		service.accountsCache = map[string]cachedAccountList{}
	}
	service.accountsCache[scope] = cachedAccountList{views, service.now().Add(service.tuning().accountsCache())}
}

func (service *service) dropAccountsCache() {
	service.accountsMu.Lock()
	defer service.accountsMu.Unlock()
	service.accountsCache = nil
	service.accountsEpoch++
}

func (service *service) accountsGeneration() uint64 {
	service.accountsMu.Lock()
	defer service.accountsMu.Unlock()
	return service.accountsEpoch
}

// updateCachedAccount puts one account's live reading into every cached
// listing that shows it, so reading one card does not send the next dashboard
// read back to inspecting every account. It also moves the epoch on, so a
// listing inspected before the reading cannot overwrite it.
func (service *service) updateCachedAccount(view accountView) {
	service.accountsMu.Lock()
	defer service.accountsMu.Unlock()
	service.accountsEpoch++
	for scope, cached := range service.accountsCache {
		for index, listed := range cached.views {
			if listed.ID == view.ID {
				cached.views = slices.Clone(cached.views)
				cached.views[index] = view
				service.accountsCache[scope] = cached
				break
			}
		}
	}
}

type listedAccount struct {
	entry   hostEntry
	record  storageRecord
	enabled bool
	err     error
}

// listedAccounts reads the records behind the host entries. They come through
// the listing call's host callback, which ends with the call, so they are read
// before an inspection that may outlive it.
func (service *service) listedAccounts(callbackID string, entries []hostEntry) []listedAccount {
	listed := make([]listedAccount, len(entries))
	for index, entry := range entries {
		record, enabled, err := service.getRecord(callbackID, entry)
		listed[index] = listedAccount{entry, record, enabled, err}
	}
	return listed
}

// inspectListed inspects the accounts concurrently, because each costs two
// independent upstream round trips; the indexed slice keeps the host entry
// order.
func (service *service) inspectListed(ctx context.Context, listed []listedAccount) []accountView {
	accounts := make([]accountView, len(listed))
	var inspections sync.WaitGroup
	for index, item := range listed {
		inspections.Add(1)
		go func() {
			defer inspections.Done()
			if item.err != nil {
				accounts[index] = failedAccount(accountView{ID: item.entry.ID, Label: "Unavailable account", Enabled: !item.entry.Disabled, Models: []accountModelView{}, ObservedAt: float64(service.now().UnixMilli()) / 1000}, item.err)
				return
			}
			accounts[index] = service.inspectAccount(ctx, item.record, item.enabled)
		}()
	}
	inspections.Wait()
	return accounts
}

// accountsRefresh is one inspection of a scope's accounts for one cache
// epoch. views is set before done closes, and stays nil when a quiesce or
// shutdown cut the inspection short.
type accountsRefresh struct {
	epoch uint64
	done  chan struct{}
	views []accountView
}

// refreshAccounts inspects a scope's accounts once per cache epoch, shared by
// every read of that epoch that arrives while it runs; epoch is taken before
// listed was read, so a write that lands meanwhile keeps the inspection out of
// the cache and out of later reads. It does not end with the read that
// started it: a caller that gives up still leaves the listing cached for the
// next read. It joins the lifecycle so a quiesce or shutdown waits for it, and
// their closing signal cancels its upstream reads.
func (service *service) refreshAccounts(scope string, epoch uint64, listed []listedAccount) *accountsRefresh {
	service.accountsMu.Lock()
	if running := service.accountsRefreshing[scope]; running != nil && running.epoch == epoch {
		service.accountsMu.Unlock()
		return running
	}
	refresh := &accountsRefresh{epoch: epoch, done: make(chan struct{})}
	if service.accountsRefreshing == nil {
		service.accountsRefreshing = map[string]*accountsRefresh{}
	}
	service.accountsRefreshing[scope] = refresh
	service.accountsMu.Unlock()
	finish := func(views []accountView) {
		service.accountsMu.Lock()
		if service.accountsRefreshing[scope] == refresh {
			delete(service.accountsRefreshing, scope)
		}
		refresh.views = views
		service.accountsMu.Unlock()
		close(refresh.done)
	}
	if service.lifecycle.enter() != nil {
		finish(nil)
		return refresh
	}
	closing := service.lifecycle.closingSignal()
	run := func() {
		defer service.lifecycle.leave()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			select {
			case <-closing:
				cancel()
			case <-ctx.Done():
			}
		}()
		views := service.inspectListed(ctx, listed)
		if ctx.Err() != nil {
			finish(nil)
			return
		}
		service.storeAccounts(scope, views, epoch)
		finish(views)
	}
	if service.background != nil {
		service.background(run)
	} else {
		go run()
	}
	return refresh
}
