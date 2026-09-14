package main

import (
	"context"
	"encoding/json"
	"strconv"
	"time"
)

type quotaSubscription struct {
	Plan     string `json:"plan,omitempty"`
	TierName string `json:"tierName,omitempty"`
	TierID   string `json:"tierId,omitempty"`
}

type quotaBucket struct {
	Window            string  `json:"window,omitempty"`
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime,omitempty"`
	Description       string  `json:"description,omitempty"`
}

type quotaGroup struct {
	DisplayName string        `json:"displayName,omitempty"`
	Buckets     []quotaBucket `json:"buckets,omitempty"`
}

type quotaFetchResponse struct {
	Subscription *quotaSubscription `json:"subscription,omitempty"`
	Groups       []quotaGroup       `json:"groups,omitempty"`
}

type quotaDescription struct {
	SupportedProviders []string `json:"supported_providers"`
	DisplayName        string   `json:"display_name"`
	SupportsReset      bool     `json:"supports_reset"`
}

// quotaFetch propagates resolve and usage errors instead of returning an empty
// quota, because an empty response renders as full remaining capacity.
func (service *service) quotaFetch(ctx context.Context, raw []byte) (interface{}, error) {
	var request struct {
		AuthID         string `json:"auth_id"`
		Provider       string `json:"provider"`
		StorageJSON    []byte `json:"storage_json"`
		HostCallbackID string `json:"host_callback_id"`
	}
	if json.Unmarshal(raw, &request) != nil {
		return nil, failure(400, "invalid_quota_request")
	}
	if request.Provider != "" && request.Provider != provider {
		return nil, failure(400, "unsupported_quota_provider")
	}
	record, token, err := service.quotaCredential(ctx, request.HostCallbackID, request.AuthID, request.StorageJSON)
	if err != nil {
		return nil, err
	}
	measured, err := service.usage(ctx, record.TokenRef, token)
	if err != nil {
		return nil, err
	}
	return quotaFromUsage(measured), nil
}

func (service *service) quotaCredential(ctx context.Context, callbackID, authID string, storage []byte) (storageRecord, sessionToken, error) {
	if len(storage) != 0 {
		return service.resolve(ctx, storage, authID)
	}
	if callbackID == "" {
		return storageRecord{}, sessionToken{}, failure(401, "authenticated_management_callback_required")
	}
	record, _, err := service.findRecord(callbackID, authID)
	if err != nil {
		return storageRecord{}, sessionToken{}, err
	}
	token, err := service.inspectLocalAccount(ctx, record)
	return record, token, err
}

func quotaFromUsage(measured *usageView) quotaFetchResponse {
	response := quotaFetchResponse{}
	if measured == nil {
		return response
	}
	if measured.Tier != nil || measured.TierCode != nil {
		subscription := quotaSubscription{}
		if measured.Tier != nil {
			subscription.Plan, subscription.TierName = *measured.Tier, *measured.Tier
		}
		if measured.TierCode != nil {
			subscription.TierID = strconv.Itoa(*measured.TierCode)
		}
		response.Subscription = &subscription
	}
	buckets := make([]quotaBucket, 0, len(measured.Metrics))
	for _, metric := range measured.Metrics {
		bucket := quotaBucket{Window: metric.WindowKind, RemainingFraction: 1}
		if metric.UsageFraction != nil {
			remaining := 1 - *metric.UsageFraction
			if remaining < 0 {
				remaining = 0
			}
			if remaining > 1 {
				remaining = 1
			}
			bucket.RemainingFraction = remaining
		}
		if metric.ResetUnixSeconds != nil {
			bucket.ResetTime = time.Unix(int64(*metric.ResetUnixSeconds), 0).UTC().Format(time.RFC3339)
		}
		if metric.RemainingUnits != nil {
			bucket.Description = strconv.FormatFloat(*metric.RemainingUnits, 'f', -1, 64) + " " + metric.Unit + " remaining"
		}
		buckets = append(buckets, bucket)
	}
	if len(buckets) != 0 {
		response.Groups = []quotaGroup{{DisplayName: "Google Web", Buckets: buckets}}
	}
	return response
}
