package main

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"testing"
	"time"
)

const flowLiveCredits = "[24245,2,3,3,null,24245,[1794739096,913000000]]"

type flowQuotaAnswer struct {
	Subscription struct {
		TierID string `json:"tierId"`
	} `json:"subscription"`
	Groups []struct {
		Buckets []struct {
			Window            string  `json:"window"`
			RemainingFraction float64 `json:"remainingFraction"`
			ResetTime         string  `json:"resetTime"`
		} `json:"buckets"`
	} `json:"groups"`
	Summary []struct {
		Key    string  `json:"key"`
		Value  float64 `json:"value"`
		Unit   string  `json:"unit"`
		Format string  `json:"format"`
	} `json:"summary"`
}

func flowQuotaFetch(t *testing.T, service *service, record storageRecord) envelope {
	t.Helper()
	auth, err := authFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	return invoke(t, service, "quota.fetch", map[string]any{
		"auth_id": record.ID, "provider": provider, "storage_json": auth.StorageJSON, "host_callback_id": "fixture-scope",
	})
}

func flowQuotaRead(t *testing.T, result envelope) flowQuotaAnswer {
	t.Helper()
	if !result.OK {
		t.Fatalf("quota fetch failed: %+v", result.Error)
	}
	var answer flowQuotaAnswer
	if err := json.Unmarshal(result.Result, &answer); err != nil {
		t.Fatal(err)
	}
	return answer
}

func flowCreditsReply(t *testing.T, fixture *flowFixture, payload string) {
	t.Helper()
	var decoded any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	fixture.replies["nzlxg"] = []string{rpcEnvelope(t, "nzlxg", decoded)}
	fixture.mu.Unlock()
}

func flowBusyFor(fixture *flowFixture, answers int) {
	fixture.mu.Lock()
	fixture.busyRPC, fixture.busyRemaining = "nzlxg", answers
	fixture.mu.Unlock()
}

// flowClock makes the lease wait advance a fake clock instead of sleeping and
// counts how often the lookup waited.
func flowClock(service *service) *int {
	now := time.Unix(1792000000, 0).UTC()
	waits := 0
	service.now = func() time.Time { return now }
	service.flowWait = func(_ context.Context, interval time.Duration) error {
		waits++
		now = now.Add(interval)
		return nil
	}
	return &waits
}

func TestFlowRegistersAsTheQuotaProviderForItsCredentials(t *testing.T) {
	service := newService(nil)
	registered := invoke(t, service, "plugin.register", nil)
	var registration struct {
		Capabilities struct {
			Quota bool `json:"quota_provider"`
		} `json:"capabilities"`
	}
	if !registered.OK || json.Unmarshal(registered.Result, &registration) != nil || !registration.Capabilities.Quota {
		t.Fatal("the quota provider capability is not declared")
	}
	var identity struct {
		Identifier string `json:"identifier"`
	}
	identified := invoke(t, service, "quota.identifier", struct{}{})
	if !identified.OK || json.Unmarshal(identified.Result, &identity) != nil || identity.Identifier != provider {
		t.Fatalf("quota identifier = %s", identified.Result)
	}
	var description quotaDescription
	described := invoke(t, service, "quota.describe", struct{}{})
	if !described.OK || json.Unmarshal(described.Result, &description) != nil ||
		!slices.Equal(description.SupportedProviders, []string{provider}) || description.SupportsReset {
		t.Fatalf("quota description = %s", described.Result)
	}
}

func TestFlowQuotaReportsTheMonthlyGrantWithItsRefreshTime(t *testing.T) {
	fixture := newFlowFixture(t)
	flowCreditsReply(t, fixture, flowLiveCredits)
	service, record := flowService(t, fixture)

	quota := flowQuotaRead(t, flowQuotaFetch(t, service, record))

	if quota.Subscription.TierID != "3" || len(quota.Summary) != 0 || len(quota.Groups) != 1 || len(quota.Groups[0].Buckets) != 1 {
		t.Fatalf("quota = %+v", quota)
	}
	bucket := quota.Groups[0].Buckets[0]
	refresh := time.Unix(1794739096, 913000000).UTC().Format(time.RFC3339)
	if bucket.Window != "monthly" || math.Abs(bucket.RemainingFraction-24245.0/25000.0) > 1e-9 || bucket.ResetTime != refresh {
		t.Fatalf("monthly window = %+v, want refresh %s", bucket, refresh)
	}
}

func TestFlowQuotaReportsAICreditsBesideAFullGrant(t *testing.T) {
	fixture := newFlowFixture(t)
	flowCreditsReply(t, fixture, "[25113,2,3,3,100,25013,[1794739096,0]]")
	service, record := flowService(t, fixture)

	quota := flowQuotaRead(t, flowQuotaFetch(t, service, record))

	if len(quota.Groups) != 1 || quota.Groups[0].Buckets[0].RemainingFraction != 1 {
		t.Fatalf("a balance above the grant = %+v", quota.Groups)
	}
	if len(quota.Summary) != 1 || quota.Summary[0].Key != "ai_credits" || quota.Summary[0].Value != 100 {
		t.Fatalf("AI credits = %+v", quota.Summary)
	}
}

func TestFlowQuotaKeepsATierWithoutAKnownGrantAsABalance(t *testing.T) {
	fixture := newFlowFixture(t)
	flowCreditsReply(t, fixture, "[50,3,0,1]")
	service, record := flowService(t, fixture)

	quota := flowQuotaRead(t, flowQuotaFetch(t, service, record))

	if len(quota.Groups) != 0 || len(quota.Summary) != 1 {
		t.Fatalf("quota = %+v", quota)
	}
	if metric := quota.Summary[0]; metric.Key != "credits" || metric.Value != 50 || metric.Unit != "credits" || metric.Format != "number" {
		t.Fatalf("credit metric = %+v", metric)
	}
}

func TestFlowQuotaFailsInsteadOfReportingAnUnreadBalance(t *testing.T) {
	fixture := newFlowFixture(t)
	fixture.mu.Lock()
	fixture.replies["nzlxg"] = []string{flowErrorEnvelope(t, []any{"er", nil, nil, nil, nil, 401, "generic"})}
	fixture.mu.Unlock()
	service, record := flowService(t, fixture)

	result := flowQuotaFetch(t, service, record)

	if result.OK || result.Error.Code != "flow_unauthenticated" {
		t.Fatalf("failed credit lookup = ok %v, %s", result.OK, result.Result)
	}
}

func TestFlowQuotaAnswersABusyLeaseWithTheCurrentReading(t *testing.T) {
	// Given: a reading taken before a Gemini turn took the source account's lease.
	fixture := newFlowFixture(t)
	flowCreditsReply(t, fixture, flowLiveCredits)
	service, record := flowService(t, fixture)
	waits := flowClock(service)
	before := flowQuotaRead(t, flowQuotaFetch(t, service, record))
	flowBusyFor(fixture, 1000)

	// When: the host asks for the quota while the turn runs.
	during := flowQuotaRead(t, flowQuotaFetch(t, service, record))

	// Then: the reading answers at once, since a Gemini turn spends no Flow credits.
	if *waits != 0 || during.Groups[0].Buckets[0].RemainingFraction != before.Groups[0].Buckets[0].RemainingFraction {
		t.Fatalf("busy lookup waited %d times and answered %+v", *waits, during)
	}
}

func TestFlowQuotaRereadsTheBalanceAfterFlowMaySpendCredits(t *testing.T) {
	// Given: a reading, then a Flow request that may spend credits.
	fixture := newFlowFixture(t)
	flowCreditsReply(t, fixture, flowLiveCredits)
	service, record := flowService(t, fixture)
	waits := flowClock(service)
	flowQuotaRead(t, flowQuotaFetch(t, service, record))
	invoke(t, service, "frontend_http.handle", flowHTTPRequest{Method: "POST", Path: "/v1/flow/projects"})
	flowCreditsReply(t, fixture, "[20000,2,3,3,null,20000,[1794739096,0]]")
	flowBusyFor(fixture, 2)

	// When: the quota is read while the lease is briefly busy.
	quota := flowQuotaRead(t, flowQuotaFetch(t, service, record))

	// Then: the lookup waits for the lease and reports the new balance.
	if *waits == 0 || quota.Groups[0].Buckets[0].RemainingFraction != 0.8 {
		t.Fatalf("lookup after a spend waited %d times and answered %+v", *waits, quota)
	}
}

func TestFlowQuotaAnswersTheLastReadingWhenTheTurnOutlastsTheWait(t *testing.T) {
	fixture := newFlowFixture(t)
	flowCreditsReply(t, fixture, flowLiveCredits)
	service, record := flowService(t, fixture)
	waits := flowClock(service)
	before := flowQuotaRead(t, flowQuotaFetch(t, service, record))
	service.markFlowSpend()
	flowBusyFor(fixture, 1000)

	quota := flowQuotaRead(t, flowQuotaFetch(t, service, record))

	if want := int(flowQuotaLeaseWait / flowLeasePoll); *waits != want {
		t.Fatalf("lookup waited %d times, want %d", *waits, want)
	}
	if quota.Groups[0].Buckets[0].RemainingFraction != before.Groups[0].Buckets[0].RemainingFraction {
		t.Fatalf("last reading = %+v", quota)
	}
}

func TestFlowQuotaReportsTheBusyLeaseWithoutAnyReading(t *testing.T) {
	fixture := newFlowFixture(t)
	service, record := flowService(t, fixture)
	flowClock(service)
	flowBusyFor(fixture, 1000)

	result := flowQuotaFetch(t, service, record)

	if result.OK || result.Error.Code != "flow_session_exchange_busy" {
		t.Fatalf("busy lease without a reading = ok %v, %+v", result.OK, result.Error)
	}
}

func TestFlowQuotaRefusesCredentialsItDoesNotServe(t *testing.T) {
	fixture := newFlowFixture(t)
	service, record := flowService(t, fixture)

	other := record
	other.ID = "flow2api-other.json"
	auth, err := authFromRecord(other)
	if err != nil {
		t.Fatal(err)
	}
	mismatch := invoke(t, service, "quota.fetch", map[string]any{"auth_id": record.ID, "provider": provider, "storage_json": auth.StorageJSON})
	if mismatch.OK || mismatch.Error.Code != "flow_auth_identity_mismatch" {
		t.Fatalf("mismatched credential = %+v", mismatch.Error)
	}

	service.config.FlowAccounts = nil
	if unlisted := flowQuotaFetch(t, service, record); unlisted.OK || unlisted.Error.Code != "flow_account_not_enabled" {
		t.Fatalf("account outside the Flow allowlist = %+v", unlisted.Error)
	}
}
