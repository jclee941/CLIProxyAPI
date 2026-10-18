package main

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"
)

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

func TestFlowQuotaReportsTheCreditBalanceAsASummaryMetric(t *testing.T) {
	fixture := newFlowFixture(t)
	service, record := flowService(t, fixture)

	result := flowQuotaFetch(t, service, record)

	if !result.OK {
		t.Fatalf("quota fetch failed: %+v", result.Error)
	}
	var quota struct {
		Subscription struct {
			TierID string `json:"tierId"`
		} `json:"subscription"`
		Summary []struct {
			Key    string  `json:"key"`
			Value  float64 `json:"value"`
			Unit   string  `json:"unit"`
			Format string  `json:"format"`
		} `json:"summary"`
		Groups []json.RawMessage `json:"groups"`
	}
	if err := json.Unmarshal(result.Result, &quota); err != nil {
		t.Fatal(err)
	}
	if quota.Subscription.TierID != "3" || len(quota.Summary) != 1 || len(quota.Groups) != 0 {
		t.Fatalf("quota = %s", result.Result)
	}
	if metric := quota.Summary[0]; metric.Key != "credits" || metric.Value != 142 || metric.Unit != "credits" || metric.Format != "number" {
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

func TestFlowQuotaAnswersABusyLeaseWithoutWaiting(t *testing.T) {
	// Given: a Gemini generation holding the source account's lease.
	fixture := newFlowFixture(t)
	fixture.mu.Lock()
	fixture.busyRPC, fixture.busyRemaining = "nzlxg", 1
	fixture.mu.Unlock()
	service, record := flowService(t, fixture)
	service.flowWait = func(context.Context, time.Duration) error {
		t.Error("the quota lookup waited for the lease")
		return nil
	}

	// When: the host asks for the credential's quota.
	result := flowQuotaFetch(t, service, record)

	// Then: the lookup reports the busy lease at once.
	if result.OK || result.Error.Code != "flow_session_exchange_busy" {
		t.Fatalf("busy lease = ok %v, %+v", result.OK, result.Error)
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
