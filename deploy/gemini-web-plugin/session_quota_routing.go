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
	// roomUnits is what the five hour window has left when an observation names
	// it. A native room pins every turn to the account that opens it, so this is
	// all a new room can still spend there.
	roomUnits    float64
	roomMeasured bool
	// noVideoStreak counts the video turns in a row the account answered without
	// starting a video, as a text model or with an empty reply. noVideoUntil keeps
	// it out of new rooms for as long as that run warrants; its pinned room and
	// its quota are untouched.
	noVideoStreak int
	noVideoUntil  int64
}

// noVideoHold is how long an account sits out of new rooms after answering
// streak video turns in a row without starting a video. Over 1,184 production
// turns from 2026-09-18 to 09-25 the next turn on the same account made a video
// 45% of the time after one such answer, 36% after two, 29% after three and 10%
// after six or more, against 38% for any turn. On 09-25 two accounts answered
// every turn that way for ten hours with allowance left in both windows, and a
// fresh five hour window brought neither back. One answer holds nothing; from
// the second the hold doubles from fifteen minutes up to two hours, and a video
// ends it.
func noVideoHold(streak int) time.Duration {
	if streak < 2 {
		return 0
	}
	return 15 * time.Minute << min(streak-2, 3)
}

// roomHeadroomUnits is the five hour allowance a new native room needs. A room is
// three turns pinned to one account and a turn spent 3,000 to 4,500 units on
// 2026-09-25, so an account below this starts a room it cannot finish, and the
// turns after its allowance runs out come back as quota_exhausted or text answers.
const roomHeadroomUnits = 15000

// answersVideo reports whether an account is not sitting out new rooms for
// answering video turns without starting a video.
func (service *service) answersVideo(id string) bool {
	service.quota.mu.RLock()
	snapshot, found := service.quota.entries[id]
	service.quota.mu.RUnlock()
	return !found || service.now().Unix() >= snapshot.noVideoUntil
}

// fitsARoom reports whether an account has the allowance to finish a new room.
// An account with no observation stays eligible, as the rotation treats it.
func (service *service) fitsARoom(id string) bool {
	service.quota.mu.RLock()
	snapshot, found := service.quota.entries[id]
	service.quota.mu.RUnlock()
	return !found || !snapshot.roomMeasured || snapshot.roomUnits >= roomHeadroomUnits
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
		if metric.WindowKind == "5h" && metric.RemainingUnits != nil {
			snapshot.roomUnits = *metric.RemainingUnits
			snapshot.roomMeasured = true
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
	previous := service.quota.entries[id]
	snapshot.limitedUntil = previous.limitedUntil
	snapshot.noVideoStreak, snapshot.noVideoUntil = previous.noVideoStreak, previous.noVideoUntil
	service.quota.entries[id] = snapshot
}

// noteVideoRefusal takes the product at its word when it answers a video turn
// by saying the account has no video left. The windows are no evidence either
// way: an account reading 96% of its five hours was told the same, and new
// turns kept landing on it, each spending twenty seconds to hear it again.
func (service *service) noteVideoRefusal(id string, err error) {
	var public *publicError
	if !errors.As(err, &public) || public.Code != "no_video_generated" {
		return
	}
	// An empty reply carries only the code, and like an answer written as a text
	// model it says the product never started a video on the turn.
	if public.Message == public.Code || webTextModelReply(public.Message) {
		service.noteNoVideo(id)
		return
	}
	if !webVideoLimitReply(public.Message) {
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

// noteNoVideo extends an account's run of video turns answered without a video
// and holds it out of new rooms for noVideoHold. It does not touch quota
// availability, so a room already pinned there keeps its turns.
func (service *service) noteNoVideo(id string) {
	now := service.now()
	service.quota.mu.Lock()
	defer service.quota.mu.Unlock()
	if service.quota.entries == nil {
		service.quota.entries = make(map[string]quotaSnapshot)
	}
	snapshot := service.quota.entries[id]
	snapshot.noVideoStreak++
	snapshot.noVideoUntil = now.Add(noVideoHold(snapshot.noVideoStreak)).Unix()
	service.quota.entries[id] = snapshot
}

// noteVideoDelivered ends an account's run of answers without a video, and the
// hold that run put on it.
func (service *service) noteVideoDelivered(id string) {
	service.quota.mu.Lock()
	defer service.quota.mu.Unlock()
	snapshot, found := service.quota.entries[id]
	if !found {
		return
	}
	snapshot.noVideoStreak, snapshot.noVideoUntil = 0, 0
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
