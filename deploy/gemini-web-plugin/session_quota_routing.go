package main

import (
	"context"
	"sync"
)

type quotaSnapshot struct {
	exhausted bool
	resetAt   int64
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
	snapshot := quotaSnapshot{}
	unknownReset := false
	for _, metric := range usage.Metrics {
		if metric.WindowKind != "5h" && metric.WindowKind != "weekly" {
			continue
		}
		if metric.UsageFraction != nil && *metric.UsageFraction >= 1 ||
			metric.UsagePercent != nil && *metric.UsagePercent >= 100 ||
			metric.RemainingUnits != nil && *metric.RemainingUnits <= 0 {
			snapshot.exhausted = true
			if metric.ResetUnixSeconds == nil || *metric.ResetUnixSeconds <= 0 {
				unknownReset = true
			} else if int64(*metric.ResetUnixSeconds) > snapshot.resetAt {
				snapshot.resetAt = int64(*metric.ResetUnixSeconds)
			}
		}
	}
	if unknownReset {
		snapshot.resetAt = 0
	}
	service.quota.mu.Lock()
	defer service.quota.mu.Unlock()
	if service.quota.entries == nil {
		service.quota.entries = make(map[string]quotaSnapshot)
	}
	service.quota.entries[id] = snapshot
}

// Exhaustion remains authoritative until reset or a new observation. A cache
// age limit must not put a still-exhausted account back into rotation.
func (service *service) quotaAvailable(id string) bool {
	service.quota.mu.RLock()
	snapshot, found := service.quota.entries[id]
	service.quota.mu.RUnlock()
	return !found || !snapshot.exhausted || snapshot.resetAt > 0 && service.now().Unix() >= snapshot.resetAt
}

func (service *service) refreshQuota(ctx context.Context, record storageRecord, token sessionToken) {
	usage, err := service.usage(ctx, record.TokenRef, token)
	if err != nil {
		return
	}
	service.observeQuota(record.ID, usage)
}
