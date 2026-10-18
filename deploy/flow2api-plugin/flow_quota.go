package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
)

type quotaSubscription struct {
	TierID string `json:"tierId,omitempty"`
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

type quotaMetric struct {
	Key    string  `json:"key"`
	Label  string  `json:"label"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit,omitempty"`
	Format string  `json:"format,omitempty"`
}

type quotaFetchResponse struct {
	Subscription *quotaSubscription `json:"subscription,omitempty"`
	Groups       []quotaGroup       `json:"groups,omitempty"`
	Summary      []quotaMetric      `json:"summary,omitempty"`
}

type quotaDescription struct {
	SupportedProviders []string `json:"supported_providers"`
	DisplayName        string   `json:"display_name"`
	SupportsReset      bool     `json:"supports_reset"`
}

// flowMonthlyCredits is the monthly Google Flow credit grant per paygate tier,
// from the credit table in the Google Flow Help Center
// (https://support.google.com/flow/answer/16526234): Google AI Pro, paygate
// tier ONE (1), gets 1,000 and Google AI Ultra, paygate tier TWO (2), 25,000.
// Other tiers have no grant this plugin can confirm and report their balance.
var flowMonthlyCredits = map[int]int{1: 1000, 2: 25000}

// flowQuotaLeaseWait keeps a lookup that waits for a busy lease inside the
// time a management request survives behind the public tunnel.
const flowQuotaLeaseWait = 60 * time.Second

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
	balance, err := service.flowBalance(withoutLeaseWait(ctx), record)
	if safeCredentialCode(err) == "flow_session_exchange_busy" {
		balance, err = service.flowBalanceBehindLease(ctx, record)
	}
	if err != nil {
		return nil, err
	}
	return flowQuota(balance), nil
}

// flowBalanceBehindLease answers while a Gemini turn holds the source account's
// lease. Such a turn spends no Flow credits, so a reading taken since Flow last
// ran is still the balance. Otherwise the lookup waits for the lease and, when
// the turn outlasts the wait, answers with the last reading and its age.
func (service *service) flowBalanceBehindLease(ctx context.Context, record storageRecord) (flowReading, error) {
	last, known := service.lastFlowReading(record.SourceAuthID)
	if known && last.current {
		return last, nil
	}
	balance, err := service.flowBalance(withLeaseWait(ctx, flowQuotaLeaseWait), record)
	if known && safeCredentialCode(err) == "flow_session_exchange_busy" {
		return last, nil
	}
	return balance, err
}

func flowQuota(balance flowReading) quotaFetchResponse {
	response := quotaFetchResponse{}
	if balance.tier != nil {
		response.Subscription = &quotaSubscription{TierID: strconv.Itoa(*balance.tier)}
	}
	flowCredits := balance.credits
	if balance.flowCredits != nil {
		flowCredits = *balance.flowCredits
	}
	if grant, granted := flowMonthlyCredits[balance.paygate]; granted {
		bucket := quotaBucket{
			Window:            "monthly",
			RemainingFraction: math.Min(1, float64(flowCredits)/float64(grant)),
			Description:       fmt.Sprintf("%d / %d Google Flow credits", flowCredits, grant),
		}
		if !balance.refreshAt.IsZero() {
			bucket.ResetTime = balance.refreshAt.Format(time.RFC3339)
		}
		if !balance.current {
			bucket.Description += ", read " + balance.observedAt.UTC().Format(time.RFC3339)
		}
		response.Groups = []quotaGroup{{DisplayName: "Google Flow", Buckets: []quotaBucket{bucket}}}
	} else {
		response.Summary = append(response.Summary, quotaMetric{Key: "credits", Label: "Flow credits", Value: float64(flowCredits), Unit: "credits", Format: "number"})
	}
	if balance.aiCredits != nil && *balance.aiCredits > 0 {
		response.Summary = append(response.Summary, quotaMetric{Key: "ai_credits", Label: "AI credits", Value: float64(*balance.aiCredits), Unit: "credits", Format: "number"})
	}
	return response
}
