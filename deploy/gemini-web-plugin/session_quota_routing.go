package main

import (
	"context"
	"errors"
	"sync"
	"time"
)

// videoLimitHold is how long an account the product reported out of video sits
// out when no observed window says when its allowance turns over.
const videoLimitHold = 30 * time.Minute

type quotaSnapshot struct {
	exhausted bool
	resetAt   int64
	// turnover is when the five hour window resets, the earliest a limit the
	// product reported can lift. The weekly window often reads more used
	// without being that limit; holding on it benched idle accounts for days.
	turnover int64
	// limitedUntil holds a limit the product reported itself. The windows can
	// still read below full then, since a video needs more units than are
	// left, so no later observation may release the account before it passes.
	limitedUntil int64
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
		if metric.WindowKind == "5h" && metric.ResetUnixSeconds != nil {
			snapshot.turnover = int64(*metric.ResetUnixSeconds)
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
	snapshot.limitedUntil = service.quota.entries[id].limitedUntil
	service.quota.entries[id] = snapshot
}

// noteVideoRefusal takes the product at its word when it answers a video turn
// by saying the account has no video left. The windows are no evidence either
// way: an account reading 96% of its five hours was told the same, and new
// turns kept landing on it, each spending twenty seconds to hear it again.
func (service *service) noteVideoRefusal(id string, err error) {
	var public *publicError
	if !errors.As(err, &public) || public.Code != "no_video_generated" || !webVideoLimitReply(public.Message) {
		return
	}
	now := service.now().Unix()
	service.quota.mu.Lock()
	defer service.quota.mu.Unlock()
	if service.quota.entries == nil {
		service.quota.entries = make(map[string]quotaSnapshot)
	}
	snapshot := service.quota.entries[id]
	snapshot.limitedUntil = snapshot.turnover
	if snapshot.limitedUntil <= now {
		snapshot.limitedUntil = now + int64(videoLimitHold/time.Second)
	}
	service.quota.entries[id] = snapshot
}

// Exhaustion remains authoritative until reset or a new observation. A cache
// age limit must not put a still-exhausted account back into rotation, and a
// limit the product reported holds until the window it named turns over.
func (service *service) quotaAvailable(id string) bool {
	service.quota.mu.RLock()
	snapshot, found := service.quota.entries[id]
	service.quota.mu.RUnlock()
	now := service.now().Unix()
	if !found {
		return true
	}
	if now < snapshot.limitedUntil {
		return false
	}
	return !snapshot.exhausted || snapshot.resetAt > 0 && now >= snapshot.resetAt
}

func (service *service) refreshQuota(ctx context.Context, record storageRecord, token sessionToken) {
	usage, err := service.usage(ctx, record.TokenRef, token)
	if err != nil {
		return
	}
	service.observeQuota(record.ID, usage)
}
