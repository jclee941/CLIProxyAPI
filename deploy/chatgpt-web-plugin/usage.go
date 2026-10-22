package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type webLimit struct {
	FeatureName string   `json:"feature_name"`
	Remaining   *float64 `json:"remaining"`
	ResetAfter  string   `json:"reset_after"`
}

type webModelLimit struct {
	ModelSlug   string   `json:"model_slug"`
	Limit       *float64 `json:"limit"`
	ResetsAfter string   `json:"resets_after"`
}

type webBlockedFeature struct {
	Name            string   `json:"name"`
	Limit           *float64 `json:"limit"`
	ResetsAfter     string   `json:"resets_after"`
	ResetsAfterText string   `json:"resets_after_text"`
	Description     string   `json:"description"`
}

func webQuotaLimits(raw []byte) []webQuotaLimit {
	if len(raw) == 0 {
		return nil
	}
	var payload struct {
		LimitsProgress  []webLimit          `json:"limits_progress"`
		ModelLimits     []webModelLimit     `json:"model_limits"`
		BlockedFeatures []webBlockedFeature `json:"blocked_features"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	limits := make([]webQuotaLimit, 0, len(payload.LimitsProgress)+len(payload.ModelLimits))
	for _, limit := range payload.LimitsProgress {
		if limit.FeatureName == "" || limit.Remaining == nil {
			continue
		}
		limits = append(limits, webQuotaLimit{Name: limit.FeatureName, Remaining: limit.Remaining, ResetTime: limit.ResetAfter})
	}
	blocked := map[string]webBlockedFeature{}
	for _, feature := range payload.BlockedFeatures {
		if feature.Name != "" {
			blocked[feature.Name] = feature
		}
	}
	for _, limit := range payload.ModelLimits {
		if limit.ModelSlug == "" {
			continue
		}
		name := limit.ModelSlug
		if name == webProModel {
			name = webAppProModel
		}
		view := webQuotaLimit{Name: name, Limit: limit.Limit, ResetTime: limit.ResetsAfter}
		if reason, ok := blocked["reason"]; ok && (limit.ModelSlug == webProModel || strings.HasSuffix(limit.ModelSlug, "-pro")) {
			view.Reduced = true
			if reason.Limit != nil {
				view.Limit = reason.Limit
			}
			if reason.ResetsAfter != "" {
				view.ResetTime = reason.ResetsAfter
			}
		}
		limits = append(limits, view)
	}
	for index := range limits {
		if parsed, err := time.Parse(time.RFC3339, limits[index].ResetTime); err == nil {
			limits[index].ResetTime = parsed.UTC().Format(time.RFC3339)
		} else {
			limits[index].ResetTime = ""
		}
	}
	return limits
}

func quotaFromParts(usageRaw, webRaw []byte) (quotaFetchResponse, error) {
	response, err := quotaFromUsage(usageRaw)
	if err != nil {
		return quotaFetchResponse{}, err
	}
	response.WebLimits = webQuotaLimits(webRaw)
	for _, limit := range response.WebLimits {
		if limit.Remaining != nil {
			response.Summary = append(response.Summary, quotaMetric{
				Key: "web_" + limit.Name + "_remaining", Label: "ChatGPT Web · " + limit.Name,
				Value: *limit.Remaining, Unit: "remaining", Format: "number",
			})
		}
		if limit.Limit != nil {
			label := "ChatGPT Web · " + limit.Name + " limit"
			if limit.Reduced {
				label += " (capabilities reduced)"
			}
			response.Summary = append(response.Summary, quotaMetric{
				Key: "web_" + limit.Name + "_limit", Label: label,
				Value: *limit.Limit, Unit: "requests", Format: "number",
			})
		}
	}
	return response, nil
}

type rateWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

type rateLimit struct {
	PrimaryWindow   *rateWindow `json:"primary_window"`
	SecondaryWindow *rateWindow `json:"secondary_window"`
}

type additionalLimit struct {
	LimitName string     `json:"limit_name"`
	RateLimit *rateLimit `json:"rate_limit"`
}

type whamUsage struct {
	PlanType   string            `json:"plan_type"`
	Email      string            `json:"email"`
	RateLimit  *rateLimit        `json:"rate_limit"`
	Additional []additionalLimit `json:"additional_rate_limits"`
}

func windowName(seconds int64) string {
	switch seconds {
	case 18000:
		return "5h"
	case 86400:
		return "daily"
	case 604800:
		return "weekly"
	}
	if seconds > 0 && seconds%3600 == 0 {
		return fmt.Sprintf("%dh", seconds/3600)
	}
	return fmt.Sprintf("%ds", seconds)
}

func bucketFrom(window *rateWindow, description string) (quotaBucket, bool) {
	if window == nil {
		return quotaBucket{}, false
	}
	remaining := 1 - window.UsedPercent/100
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 1 {
		remaining = 1
	}
	bucket := quotaBucket{Window: windowName(window.LimitWindowSeconds), RemainingFraction: remaining, Description: description}
	if window.ResetAt > 0 {
		bucket.ResetTime = time.Unix(window.ResetAt, 0).UTC().Format(time.RFC3339)
	}
	return bucket, true
}

func appendWindows(buckets []quotaBucket, limit *rateLimit, name string) []quotaBucket {
	if limit == nil {
		return buckets
	}
	if bucket, ok := bucketFrom(limit.PrimaryWindow, name); ok {
		buckets = append(buckets, bucket)
	}
	if bucket, ok := bucketFrom(limit.SecondaryWindow, name+" (secondary)"); ok {
		buckets = append(buckets, bucket)
	}
	return buckets
}

// The ChatGPT-web group is absent on purpose: its limits come from a
// sentinel-gated endpoint that this slice does not reach yet.
func quotaFromUsage(raw []byte) (quotaFetchResponse, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return quotaFetchResponse{}, failure(502, "usage_response_invalid")
	}
	var usage whamUsage
	if json.Unmarshal(raw, &usage) != nil || usage.PlanType == "" || usage.RateLimit == nil {
		return quotaFetchResponse{}, failure(502, "usage_response_invalid")
	}
	buckets := appendWindows(nil, usage.RateLimit, "Codex")
	for _, extra := range usage.Additional {
		name := extra.LimitName
		if name == "" {
			continue
		}
		buckets = appendWindows(buckets, extra.RateLimit, name)
	}
	response := quotaFetchResponse{Subscription: &quotaSubscription{Plan: usage.PlanType, TierName: usage.PlanType}}
	if len(buckets) != 0 {
		response.Groups = []quotaGroup{{DisplayName: "Codex", Buckets: buckets}}
	}
	return response, nil
}
