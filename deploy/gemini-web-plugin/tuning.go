package main

import (
	"reflect"
	"time"
)

// tuning holds the operator-tunable limits, windows and thresholds of the
// plugin as flat config keys. Every key is a non-negative integer that
// overrides the built-in default named in its comment; zero keeps that
// default. Each value is read where it is used, so changing one does not
// drain sessions; the trapped-session release interval takes effect when its
// loop next starts.
type tuning struct {
	// RouteRoomCeilingPercent overrides firstEntryRoomCeiling as a percentage
	// of the 5-hour window (1-100).
	RouteRoomCeilingPercent int `yaml:"route_room_ceiling_percent,omitempty"`
	// VideoLimitHoldSeconds overrides videoLimitHold.
	VideoLimitHoldSeconds int `yaml:"video_limit_hold_seconds,omitempty"`
	// RoomTurns overrides roomTurns, which also bounds a request's room_videos.
	RoomTurns int `yaml:"room_turns,omitempty"`
	// DefaultGenerationUnits overrides defaultGenerationUnits.
	DefaultGenerationUnits int `yaml:"default_generation_units,omitempty"`
	// GenerationSamples overrides generationSamples.
	GenerationSamples int `yaml:"generation_samples,omitempty"`
	// SameWindowSlackSeconds overrides sameWindowSlack.
	SameWindowSlackSeconds int `yaml:"same_window_slack_seconds,omitempty"`
	// AccountsCacheSeconds overrides accountsCacheTTL.
	AccountsCacheSeconds int `yaml:"accounts_cache_seconds,omitempty"`
	// CredentialWaitSeconds overrides credentialWait.
	CredentialWaitSeconds int `yaml:"credential_wait_seconds,omitempty"`
	// QuiesceDrainSeconds overrides quiesceDrainLimit.
	QuiesceDrainSeconds int `yaml:"quiesce_drain_seconds,omitempty"`
	// TrappedReleaseSeconds overrides trappedReleaseInterval.
	TrappedReleaseSeconds int `yaml:"trapped_release_seconds,omitempty"`
	// TurnRotationSeconds overrides turnRotationAge.
	TurnRotationSeconds int `yaml:"turn_rotation_seconds,omitempty"`
	// SlotReservationSeconds overrides slotReservation.
	SlotReservationSeconds int `yaml:"slot_reservation_seconds,omitempty"`
	// ContinuationRequestBytes overrides continuationRequestLimit.
	ContinuationRequestBytes int `yaml:"continuation_request_bytes,omitempty"`
	// ContinuationRetention overrides continuationRetention.
	ContinuationRetention int `yaml:"continuation_retention,omitempty"`
	// InteractionInputBytes overrides interactionInputLimit for interaction
	// and research requests.
	InteractionInputBytes int `yaml:"interaction_input_bytes,omitempty"`
	// VideoDiagStashLimit overrides videoDiagStashLimit.
	VideoDiagStashLimit int `yaml:"video_diag_stash_limit,omitempty"`
	// TextThinkingSlot overrides webThinkingDefault, slot 17 of a text turn
	// (0-4).
	TextThinkingSlot int `yaml:"text_thinking_slot,omitempty"`
	// MaintenanceAuthCooldownSeconds overrides maintenanceAuthCooldown.
	MaintenanceAuthCooldownSeconds int `yaml:"maintenance_auth_cooldown_seconds,omitempty"`
	// MaintenanceReadyIntervalSeconds overrides maintenanceReadyInterval.
	MaintenanceReadyIntervalSeconds int `yaml:"maintenance_ready_interval_seconds,omitempty"`
	// CredentialFenceSeconds overrides credentialFenceDuration.
	CredentialFenceSeconds int `yaml:"credential_fence_seconds,omitempty"`
	// OmniRequestBytes lowers omniRequestLimit for native Omni requests.
	OmniRequestBytes int `yaml:"omni_request_bytes,omitempty"`
	// ResearchLimitSeconds overrides webResearchLimit.
	ResearchLimitSeconds int `yaml:"research_limit_seconds,omitempty"`
	// UploadLimitBytes lowers webUploadLimit for new uploads; stored file
	// metadata keeps the built-in bound.
	UploadLimitBytes int `yaml:"upload_limit_bytes,omitempty"`
	// VideoBudgetSeconds overrides webVideoBudget.
	VideoBudgetSeconds int `yaml:"video_budget_seconds,omitempty"`
	// VideoPollSeconds overrides webVideoPollInterval.
	VideoPollSeconds int `yaml:"video_poll_seconds,omitempty"`
	// KeepAliveIntervalSeconds overrides keepAliveInterval, from the next sweep.
	KeepAliveIntervalSeconds int `yaml:"keep_alive_interval_seconds,omitempty"`
	// KeepAliveWorkers overrides keepAliveWorkers.
	KeepAliveWorkers int `yaml:"keep_alive_workers,omitempty"`
	// KeepAliveAgeSeconds overrides keepAliveAge.
	KeepAliveAgeSeconds int `yaml:"keep_alive_age_seconds,omitempty"`
}

func (tuned tuning) valid() bool {
	value := reflect.ValueOf(tuned)
	for index := 0; index < value.NumField(); index++ {
		if value.Field(index).Int() < 0 {
			return false
		}
	}
	return tuned.RouteRoomCeilingPercent <= 100 && tuned.TextThinkingSlot <= 4 && tuned.OmniRequestBytes <= omniRequestLimit && tuned.UploadLimitBytes <= webUploadLimit
}

func tunedCount(configured, fallback int) int {
	if configured > 0 {
		return configured
	}
	return fallback
}

func tunedSeconds(configured int, fallback time.Duration) time.Duration {
	if configured > 0 {
		return time.Duration(configured) * time.Second
	}
	return fallback
}

func (tuned tuning) roomCeiling() float64 {
	if tuned.RouteRoomCeilingPercent > 0 {
		return float64(tuned.RouteRoomCeilingPercent) / 100
	}
	return firstEntryRoomCeiling
}

func (tuned tuning) videoLimitHold() time.Duration {
	return tunedSeconds(tuned.VideoLimitHoldSeconds, videoLimitHold)
}

func (tuned tuning) roomTurns() int { return tunedCount(tuned.RoomTurns, roomTurns) }

func (tuned tuning) generationUnits() int {
	return tunedCount(tuned.DefaultGenerationUnits, defaultGenerationUnits)
}

func (tuned tuning) generationSamples() int {
	return tunedCount(tuned.GenerationSamples, generationSamples)
}

func (tuned tuning) sameWindowSlack() time.Duration {
	return tunedSeconds(tuned.SameWindowSlackSeconds, sameWindowSlack)
}

func (tuned tuning) accountsCache() time.Duration {
	return tunedSeconds(tuned.AccountsCacheSeconds, accountsCacheTTL)
}

func (tuned tuning) credentialWait() time.Duration {
	return tunedSeconds(tuned.CredentialWaitSeconds, credentialWait)
}

func (tuned tuning) quiesceDrain() time.Duration {
	return tunedSeconds(tuned.QuiesceDrainSeconds, quiesceDrainLimit)
}

func (tuned tuning) trappedRelease() time.Duration {
	return tunedSeconds(tuned.TrappedReleaseSeconds, trappedReleaseInterval)
}

func (tuned tuning) turnRotation() time.Duration {
	return tunedSeconds(tuned.TurnRotationSeconds, turnRotationAge)
}

func (tuned tuning) slotReservation() time.Duration {
	return tunedSeconds(tuned.SlotReservationSeconds, slotReservation)
}

func (tuned tuning) continuationRequestBytes() int {
	return tunedCount(tuned.ContinuationRequestBytes, continuationRequestLimit)
}

func (tuned tuning) continuationRetention() int {
	return tunedCount(tuned.ContinuationRetention, continuationRetention)
}

func (tuned tuning) interactionInputBytes() int {
	return tunedCount(tuned.InteractionInputBytes, interactionInputLimit)
}

func (tuned tuning) videoDiagStash() int {
	return tunedCount(tuned.VideoDiagStashLimit, videoDiagStashLimit)
}

func (tuned tuning) textThinkingSlot() int {
	return tunedCount(tuned.TextThinkingSlot, webThinkingDefault)
}

func (tuned tuning) maintenanceAuthCooldown() time.Duration {
	return tunedSeconds(tuned.MaintenanceAuthCooldownSeconds, maintenanceAuthCooldown)
}

func (tuned tuning) maintenanceReadyInterval() time.Duration {
	return tunedSeconds(tuned.MaintenanceReadyIntervalSeconds, maintenanceReadyInterval)
}

func (tuned tuning) credentialFence() time.Duration {
	return tunedSeconds(tuned.CredentialFenceSeconds, credentialFenceDuration)
}

func (tuned tuning) omniRequestBytes() int {
	return tunedCount(tuned.OmniRequestBytes, omniRequestLimit)
}

func (tuned tuning) researchLimit() time.Duration {
	return tunedSeconds(tuned.ResearchLimitSeconds, webResearchLimit)
}

func (tuned tuning) uploadLimitBytes() int { return tunedCount(tuned.UploadLimitBytes, webUploadLimit) }

func (tuned tuning) videoBudget() time.Duration {
	return tunedSeconds(tuned.VideoBudgetSeconds, webVideoBudget)
}

func (tuned tuning) videoPoll() time.Duration {
	return tunedSeconds(tuned.VideoPollSeconds, webVideoPollInterval)
}

func (tuned tuning) keepAliveSweep() time.Duration {
	return tunedSeconds(tuned.KeepAliveIntervalSeconds, keepAliveInterval)
}

func (tuned tuning) keepAliveWorkerCount() int {
	return tunedCount(tuned.KeepAliveWorkers, keepAliveWorkers)
}

func (tuned tuning) keepAliveRotationAge() time.Duration {
	return tunedSeconds(tuned.KeepAliveAgeSeconds, keepAliveAge)
}

// tuning is the tuning the last successful register applied. It is read
// without the service lock because quota, slot and lease code that holds
// other locks reads it.
func (service *service) tuning() tuning {
	if tuned := service.tuned.Load(); tuned != nil {
		return *tuned
	}
	return tuning{}
}
