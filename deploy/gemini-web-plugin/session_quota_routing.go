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
	// videoCapUntil is when the product's video budget says the account may make
	// videos again. The budget is the product answering that question directly,
	// so, unlike a limit read out of a reply, its next reading moves or lifts it.
	videoCapUntil int64
	// roomUnits and weekUnits are what the five hour and weekly windows have
	// left when an observation names them. A native room pins every turn to the
	// account that opens it, so this is all a new room can still spend there.
	roomUnits    float64
	roomMeasured bool
	weekUnits    float64
	weekMeasured bool
	// weekTurnover is when the weekly window resets. Once a window has turned
	// over, what it had left says nothing about the refilled one.
	weekTurnover int64
	// delivered counts the videos the account delivered since this reading; the
	// next reading divides what the five hour window lost by it.
	delivered int
}

// roomTurns is how many videos a native room makes on the account that opens
// it: the clip and the two extensions a scene asks for.
const roomTurns = 3

// defaultGenerationUnits stands in for the cost of a video until one has been
// measured. A turn spent 3,000 to 4,500 units on 2026-09-25, and a room was
// held to need 15,000 before the cost was measured.
const defaultGenerationUnits = 5000

// generationSamples bounds how far back the cost of a video is remembered, so a
// change in price reaches the scheduler within a few videos.
const generationSamples = 8

// sameWindowSlack is how far two readings of one five hour window may disagree
// about when it turns over.
const sameWindowSlack = 10 * time.Minute

// generationUnits is what one video costs: what the five hour window lost
// between two readings of an account, divided by the videos it delivered in
// between. The dearest recent video sets the price, so a room is never opened
// on the hope that its turns come cheap.
func (service *service) generationUnits() float64 {
	service.quota.mu.RLock()
	defer service.quota.mu.RUnlock()
	units := 0.0
	for _, sample := range service.quota.samples {
		units = max(units, sample)
	}
	if units == 0 {
		return defaultGenerationUnits
	}
	return units
}

// affordsARoom reports whether an account can pay for a whole new room in both
// windows. An account short of three videos starts a room it cannot finish, and
// the turns after its allowance runs out fail where no other account can take
// them, since the room stays pinned there. An account with no reading stays
// eligible, as the rotation treats it.
func (service *service) affordsARoom(id string) bool {
	need := roomTurns * service.generationUnits()
	service.quota.mu.RLock()
	snapshot, found := service.quota.entries[id]
	service.quota.mu.RUnlock()
	if !found {
		return true
	}
	// Only a video turn or the account listing takes a new reading, and neither
	// happens while no room opens: a reading from a window that has since turned
	// over kept every account out of new rooms for two hours on 2026-09-26, each
	// with a full window, until someone opened the listing.
	now := service.now().Unix()
	roomKnown := snapshot.roomMeasured && !turnedOver(snapshot.turnover, now)
	weekKnown := snapshot.weekMeasured && !turnedOver(snapshot.weekTurnover, now)
	return (!roomKnown || snapshot.roomUnits >= need) && (!weekKnown || snapshot.weekUnits >= need)
}

func turnedOver(reset, now int64) bool {
	return reset > 0 && now >= reset
}

type quotaCache struct {
	mu      sync.RWMutex
	entries map[string]quotaSnapshot
	// samples is the cost of the most recent videos, fleet wide.
	samples []float64
}

// observeQuota keeps what the account listing, the keep-alive sweep and every
// video turn read. The scheduler must answer without a round trip, so routing
// reads this instead of asking Google per request.
func (service *service) observeQuota(id string, usage *usageView) {
	if usage == nil {
		return
	}
	now := service.now().Unix()
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
		if metric.WindowKind == "weekly" && metric.ResetUnixSeconds != nil {
			snapshot.weekTurnover = int64(*metric.ResetUnixSeconds)
		}
		if metric.WindowKind == "weekly" && metric.RemainingUnits != nil {
			snapshot.weekUnits = *metric.RemainingUnits
			snapshot.weekMeasured = true
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
	snapshot.videoCapUntil = previous.videoCapUntil
	if usage.VideoCapped != nil {
		snapshot.videoCapUntil = 0
		if *usage.VideoCapped {
			snapshot.videoCapUntil = now + int64(videoLimitHold/time.Second)
			if usage.VideoAvailableAt != nil && int64(*usage.VideoAvailableAt) > now {
				snapshot.videoCapUntil = int64(*usage.VideoAvailableAt)
			}
		}
	}
	// A window that turned over in between says nothing about what the videos
	// cost, and neither does a reading with no video behind it.
	slack := int64(sameWindowSlack / time.Second)
	sameWindow := previous.turnover-snapshot.turnover < slack && snapshot.turnover-previous.turnover < slack
	if previous.delivered > 0 && previous.roomMeasured && snapshot.roomMeasured && sameWindow {
		if spent := previous.roomUnits - snapshot.roomUnits; spent > 0 {
			service.quota.samples = append(service.quota.samples, spent/float64(previous.delivered))
			if len(service.quota.samples) > generationSamples {
				service.quota.samples = service.quota.samples[len(service.quota.samples)-generationSamples:]
			}
		}
	}
	service.quota.entries[id] = snapshot
}

// noteVideoDelivered counts a video against the account's next reading, which
// is how the cost of a video is measured.
func (service *service) noteVideoDelivered(id string) {
	service.quota.mu.Lock()
	defer service.quota.mu.Unlock()
	if service.quota.entries == nil {
		service.quota.entries = make(map[string]quotaSnapshot)
	}
	snapshot := service.quota.entries[id]
	snapshot.delivered++
	service.quota.entries[id] = snapshot
}

// observeTurn reads the windows and the video budget on the session that just
// ran a video turn. Nothing else reads them in production - the sweep is off
// and the listing only runs when someone opens it - so the scheduler routed on
// readings hours old, and learned an account was out of videos only from the
// turn it wasted there. A reading that fails leaves the last one standing.
func (service *service) observeTurn(ctx context.Context, id string, session *webSession) {
	usage, err := service.webUsage(ctx, session)
	if err != nil {
		return
	}
	service.observeQuota(id, usage)
}

// noteVideoRefusal takes the product at its word when it answers a video turn
// by saying the account has no video left. The windows are no evidence either
// way: an account reading 96% of its five hours was told the same, and new
// turns kept landing on it, each spending twenty seconds to hear it again.
// Any other answer without a video, one written as a text model or an empty
// reply included, holds nothing: only an exhausted window or a limit the
// product names takes an account out of new rooms.
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
// age limit must not put a still-exhausted account back into rotation, a limit
// the product reported holds until the window it named turns over, and a video
// cap holds until the budget says videos are back.
func (service *service) quotaAvailable(id string) bool {
	service.quota.mu.RLock()
	snapshot, found := service.quota.entries[id]
	service.quota.mu.RUnlock()
	now := service.now().Unix()
	if !found {
		return true
	}
	if now < snapshot.limitedUntil || now < snapshot.videoCapUntil {
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
