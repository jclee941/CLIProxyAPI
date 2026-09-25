package main

import (
	"encoding/json"
	"testing"
)

type quotaFetchFixture struct {
	AuthID         string `json:"auth_id"`
	Provider       string `json:"provider"`
	StorageJSON    []byte `json:"storage_json"`
	HostCallbackID string `json:"host_callback_id"`
}

type quotaViewFixture struct {
	Subscription *struct {
		Plan     string `json:"plan"`
		TierName string `json:"tierName"`
		TierID   string `json:"tierId"`
	} `json:"subscription"`
	Groups []struct {
		DisplayName string `json:"displayName"`
		Buckets     []struct {
			Window            string  `json:"window"`
			RemainingFraction float64 `json:"remainingFraction"`
			ResetTime         string  `json:"resetTime"`
			Description       string  `json:"description"`
		} `json:"buckets"`
	} `json:"groups"`
}

func quotaFetchFor(t *testing.T, service *service, record storageRecord) envelope {
	t.Helper()
	auth, err := authFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	return invoke(t, service, "quota.fetch", quotaFetchFixture{record.ID, provider, auth.StorageJSON, "scope-quota"})
}

func TestRegisterDeclaresQuotaProvider_soManagerQuotaRefreshIsWired(t *testing.T) {
	service := newService(nil)

	result := invoke(t, service, "plugin.register", struct {
		ConfigYAML []byte `json:"config_yaml"`
	}{[]byte("enabled: true\n")})

	if !result.OK {
		t.Fatalf("register failed: %+v", result.Error)
	}
	var registration struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(result.Result, &registration); err != nil {
		t.Fatal(err)
	}
	if string(registration.Capabilities["quota_provider"]) != "true" {
		t.Fatalf("quota_provider capability = %s", registration.Capabilities["quota_provider"])
	}
}

func TestQuotaIdentifierAndDescribe_reportGeminiWebWithoutReset(t *testing.T) {
	service, _, _ := resolveFixture(t)

	identity := invoke(t, service, "quota.identifier", struct{}{})
	described := invoke(t, service, "quota.describe", struct{}{})

	if !identity.OK || !described.OK {
		t.Fatalf("identifier=%+v describe=%+v", identity.Error, described.Error)
	}
	var identifier struct {
		Identifier string `json:"identifier"`
	}
	if err := json.Unmarshal(identity.Result, &identifier); err != nil {
		t.Fatal(err)
	}
	if identifier.Identifier != provider {
		t.Fatalf("quota identifier=%q", identifier.Identifier)
	}
	var capability struct {
		SupportedProviders []string `json:"supported_providers"`
		DisplayName        string   `json:"display_name"`
		SupportsReset      bool     `json:"supports_reset"`
	}
	if err := json.Unmarshal(described.Result, &capability); err != nil {
		t.Fatal(err)
	}
	if len(capability.SupportedProviders) != 1 || capability.SupportedProviders[0] != provider || capability.DisplayName == "" || capability.SupportsReset {
		t.Fatalf("describe=%+v", capability)
	}
}

func TestQuotaFetchMapsMeasuredUsage_whenSessionIsReady(t *testing.T) {
	service, _, record := localAccountFixture(t, false)

	result := quotaFetchFor(t, service, record)

	if !result.OK {
		t.Fatalf("quota fetch failed: %+v", result.Error)
	}
	var view quotaViewFixture
	if err := json.Unmarshal(result.Result, &view); err != nil {
		t.Fatal(err)
	}
	if view.Subscription == nil || view.Subscription.TierName != "PRO" || view.Subscription.TierID != "2" {
		t.Fatalf("subscription=%+v", view.Subscription)
	}
	if len(view.Groups) != 1 || len(view.Groups[0].Buckets) != 2 || view.Groups[0].DisplayName == "" {
		t.Fatalf("groups=%+v", view.Groups)
	}
	windows := map[string]float64{}
	for _, bucket := range view.Groups[0].Buckets {
		windows[bucket.Window] = bucket.RemainingFraction
		if bucket.ResetTime == "" || bucket.Description == "" {
			t.Fatalf("bucket missing reset/description: %+v", bucket)
		}
	}
	if windows["5h"] != 0.75 || windows["weekly"] != 1 {
		t.Fatalf("remaining fractions=%v", windows)
	}
}

func TestQuotaFetchResolvesThroughHostCallback_whenStorageJSONIsAbsent(t *testing.T) {
	service, _, record := localAccountFixture(t, false)

	result := invoke(t, service, "quota.fetch", quotaFetchFixture{AuthID: record.ID, Provider: provider, HostCallbackID: "scope-quota"})

	if !result.OK {
		t.Fatalf("host-shaped quota fetch failed: %+v", result.Error)
	}
	var view quotaViewFixture
	if err := json.Unmarshal(result.Result, &view); err != nil {
		t.Fatal(err)
	}
	if view.Subscription == nil || view.Subscription.TierName != "PRO" || len(view.Groups) != 1 || len(view.Groups[0].Buckets) != 2 {
		t.Fatalf("subscription=%+v groups=%+v", view.Subscription, view.Groups)
	}
}

func TestQuotaFetchFailsClosed_whenSessionIsInterrupted(t *testing.T) {
	service, _, record := localAccountFixture(t, true)

	result := quotaFetchFor(t, service, record)

	if result.OK {
		t.Fatal("interrupted session reported quota")
	}
	if result.Error == nil || result.Error.Code != "needs_operator" {
		t.Fatalf("error=%+v", result.Error)
	}
}
