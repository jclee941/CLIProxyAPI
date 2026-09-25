package main

import (
	"sort"
	"strconv"
	"strings"
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

// cachedAccounts returns a listing that is still inside its freshness window.
// Listing one account costs two upstream round trips to Google, so repeated
// dashboard reads reuse the last result instead of re-querying every account.
func (service *service) cachedAccounts(scope string) ([]accountView, bool) {
	service.accountsMu.Lock()
	defer service.accountsMu.Unlock()
	cached, exists := service.accountsCache[scope]
	if !exists || !service.now().Before(cached.expiresAt) {
		return nil, false
	}
	return cached.views, true
}

func (service *service) storeAccounts(scope string, views []accountView) {
	service.accountsMu.Lock()
	defer service.accountsMu.Unlock()
	if service.accountsCache == nil {
		service.accountsCache = map[string]cachedAccountList{}
	}
	service.accountsCache[scope] = cachedAccountList{views, service.now().Add(accountsCacheTTL)}
}

func (service *service) dropAccountsCache() {
	service.accountsMu.Lock()
	defer service.accountsMu.Unlock()
	service.accountsCache = nil
}
