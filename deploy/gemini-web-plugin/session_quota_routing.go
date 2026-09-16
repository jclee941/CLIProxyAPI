package main

import (
	"context"
	"sync"
	"time"
)

const (
	quotaFreshness = 10 * time.Minute
	quotaExhausted = 0.98
)

type quotaSnapshot struct {
	headroom   float64
	exhausted  bool
	resetAt    int64
	observedAt int64
}

type quotaCache struct {
	mu      sync.RWMutex
	entries map[string]quotaSnapshot
}

// observeQuota keeps what the account listing and the keep-alive sweep already
// fetch. The scheduler must answer without a round trip, so routing reads this
// instead of asking Google per request.
func (service *service) observeQuota(id string, usage *usageView) {
	if usage == nil {
		return
	}
	snapshot := quotaSnapshot{headroom: 1, observedAt: service.now().Unix()}
	for _, metric := range usage.Metrics {
		if metric.WindowKind != "5h" || metric.UsageFraction == nil {
			continue
		}
		headroom := 1 - *metric.UsageFraction
		if headroom < snapshot.headroom {
			snapshot.headroom = headroom
		}
		if *metric.UsageFraction >= quotaExhausted {
			snapshot.exhausted = true
			if metric.ResetUnixSeconds != nil {
				snapshot.resetAt = int64(*metric.ResetUnixSeconds)
			}
		}
	}
	service.quota.mu.Lock()
	defer service.quota.mu.Unlock()
	if service.quota.entries == nil {
		service.quota.entries = make(map[string]quotaSnapshot)
	}
	service.quota.entries[id] = snapshot
}

// quotaHeadroom reports the share of the five hour window an account still has.
// An unmeasured account reads as full so a missing observation never demotes it
// below one that is genuinely exhausted.
func (service *service) quotaHeadroom(id string) (float64, bool) {
	service.quota.mu.RLock()
	snapshot, found := service.quota.entries[id]
	service.quota.mu.RUnlock()
	if !found || service.now().Unix()-snapshot.observedAt > int64(quotaFreshness/time.Second) {
		return 1, true
	}
	if snapshot.exhausted && (snapshot.resetAt == 0 || service.now().Unix() < snapshot.resetAt) {
		return 0, false
	}
	return snapshot.headroom, true
}

func (service *service) refreshQuota(ctx context.Context, record storageRecord, token sessionToken) {
	usage, err := service.usage(ctx, record.TokenRef, token)
	if err != nil {
		return
	}
	service.observeQuota(record.ID, usage)
}
