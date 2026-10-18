package main

import (
	"context"
	"encoding/json"
	"strconv"
)

type quotaSubscription struct {
	TierID string `json:"tierId,omitempty"`
}

type quotaMetric struct {
	Key    string  `json:"key"`
	Label  string  `json:"label"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit,omitempty"`
	Format string  `json:"format,omitempty"`
}

type quotaFetchResponse struct {
	Subscription *quotaSubscription `json:"subscription,omitempty"`
	Summary      []quotaMetric      `json:"summary"`
}

type quotaDescription struct {
	SupportedProviders []string `json:"supported_providers"`
	DisplayName        string   `json:"display_name"`
	SupportsReset      bool     `json:"supports_reset"`
}

// quotaFetch reports the credit balance of the Gemini account a Flow
// credential borrows. Flow states no allowance or reset time for it, so the
// balance is a summary metric: a quota bucket would need a remaining fraction
// Flow does not report. Like the account listing, a busy account answers at
// once instead of waiting behind a generation.
func (service *service) quotaFetch(ctx context.Context, raw []byte) (interface{}, error) {
	var request struct {
		AuthID      string `json:"auth_id"`
		Provider    string `json:"provider"`
		StorageJSON []byte `json:"storage_json"`
	}
	if json.Unmarshal(raw, &request) != nil {
		return nil, failure(400, "flow_invalid_quota_request")
	}
	if request.Provider != "" && request.Provider != provider {
		return nil, failure(400, "flow_unsupported_quota_provider")
	}
	record, err := service.parseStorage(request.StorageJSON, false)
	if err != nil {
		return nil, err
	}
	if record.ID != request.AuthID {
		return nil, failure(400, "flow_auth_identity_mismatch")
	}
	if !service.flowAccount(record.SourceAuthID) {
		return nil, failure(403, "flow_account_not_enabled")
	}
	credits, tier, err := service.flowBalance(withoutLeaseWait(ctx), record)
	if err != nil {
		return nil, err
	}
	response := quotaFetchResponse{Summary: []quotaMetric{{Key: "credits", Label: "Flow credits", Value: float64(credits), Unit: "credits", Format: "number"}}}
	if tier != nil {
		response.Subscription = &quotaSubscription{TierID: strconv.Itoa(*tier)}
	}
	return response, nil
}
