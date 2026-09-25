package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

const usageFixture = `{
 "user_id": "user-U6fVHlOWsLxqmOX4GvtHXViu",
 "account_id": "",
 "email": "jclee@jclee.me",
 "plan_type": "pro",
 "rate_limit": {
  "allowed": true,
  "limit_reached": false,
  "primary_window": {"used_percent": 100, "limit_window_seconds": 604800, "reset_after_seconds": 512847, "reset_at": 1789874379},
  "secondary_window": null
 },
 "additional_rate_limits": [
  {"limit_name": "GPT-5.3-Codex-Spark", "metered_feature": "codex_bengalfox",
   "rate_limit": {"allowed": true, "limit_reached": false,
     "primary_window": {"used_percent": 0, "limit_window_seconds": 18000, "reset_after_seconds": 18000, "reset_at": 1789379532},
     "secondary_window": {"used_percent": 0, "limit_window_seconds": 604800, "reset_after_seconds": 604800, "reset_at": 1789966332}}},
  {"limit_name": "gpt-reserve", "metered_feature": "base_model_inference",
   "rate_limit": {"allowed": true, "limit_reached": false,
     "primary_window": {"used_percent": 0, "limit_window_seconds": 604800, "reset_after_seconds": 604800, "reset_at": 1789966332},
     "secondary_window": null}}
 ],
 "credits": {"has_credits": false, "unlimited": false, "balance": "0"}
}`

func bucketsByDescription(group quotaGroup) map[string]quotaBucket {
	index := make(map[string]quotaBucket, len(group.Buckets))
	for _, bucket := range group.Buckets {
		index[bucket.Description] = bucket
	}
	return index
}

func TestQuotaFromUsageMapsCodexWindows_whenUpstreamReportsRealLimits(t *testing.T) {
	view, err := quotaFromUsage([]byte(usageFixture))

	if err != nil {
		t.Fatalf("mapping failed: %v", err)
	}
	if view.Subscription == nil || view.Subscription.Plan != "pro" || view.Subscription.TierName != "pro" {
		t.Fatalf("subscription=%+v", view.Subscription)
	}
	if len(view.Groups) != 1 || view.Groups[0].DisplayName != "Codex" {
		t.Fatalf("groups=%+v", view.Groups)
	}
	buckets := bucketsByDescription(view.Groups[0])
	primary, ok := buckets["Codex"]
	if !ok {
		t.Fatalf("primary Codex bucket missing: %+v", view.Groups[0].Buckets)
	}
	if primary.RemainingFraction != 0 {
		t.Fatalf("exhausted weekly window must report 0 remaining, got %v", primary.RemainingFraction)
	}
	if primary.Window != "weekly" {
		t.Fatalf("primary window=%q", primary.Window)
	}
	if primary.ResetTime != time.Unix(1789874379, 0).UTC().Format(time.RFC3339) {
		t.Fatalf("primary reset=%q", primary.ResetTime)
	}
	spark, ok := buckets["GPT-5.3-Codex-Spark"]
	if !ok || spark.Window != "5h" || spark.RemainingFraction != 1 {
		t.Fatalf("spark bucket=%+v ok=%t", spark, ok)
	}
	if _, ok := buckets["gpt-reserve"]; !ok {
		t.Fatalf("gpt-reserve bucket missing: %+v", view.Groups[0].Buckets)
	}
}

func TestQuotaFromUsageFailsClosed_whenPayloadIsNotUsage(t *testing.T) {
	for _, raw := range []string{``, `null`, `[]`, `{"plan_type":5}`, `{"rate_limit":"nope"}`} {
		if _, err := quotaFromUsage([]byte(raw)); err == nil {
			t.Fatalf("accepted malformed usage payload %q", raw)
		}
	}
}

func TestQuotaIdentifierAndDescribe_declareCodexSupport(t *testing.T) {
	service := newService(nil)

	identity := service.handle(context.Background(), "quota.identifier", []byte(`{}`))
	described := service.handle(context.Background(), "quota.describe", []byte(`{}`))

	var identityEnvelope, describeEnvelope envelope
	if err := json.Unmarshal(identity, &identityEnvelope); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(described, &describeEnvelope); err != nil {
		t.Fatal(err)
	}
	if !identityEnvelope.OK || !describeEnvelope.OK {
		t.Fatalf("identity=%+v describe=%+v", identityEnvelope.Error, describeEnvelope.Error)
	}
	var identifier struct {
		Identifier string `json:"identifier"`
	}
	if err := json.Unmarshal(identityEnvelope.Result, &identifier); err != nil {
		t.Fatal(err)
	}
	if identifier.Identifier != provider {
		t.Fatalf("identifier=%q", identifier.Identifier)
	}
	var capability quotaDescription
	if err := json.Unmarshal(describeEnvelope.Result, &capability); err != nil {
		t.Fatal(err)
	}
	if len(capability.SupportedProviders) != 1 || capability.SupportedProviders[0] != authProvider || capability.SupportsReset {
		t.Fatalf("describe=%+v", capability)
	}
}

func TestRegisterDeclaresQuotaProvider_soTheHostWiresIt(t *testing.T) {
	service := newService(nil)

	raw := service.handle(context.Background(), "plugin.register", []byte(`{}`))

	var response envelope
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("register failed: %+v", response.Error)
	}
	var registration struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(response.Result, &registration); err != nil {
		t.Fatal(err)
	}
	if string(registration.Capabilities["quota_provider"]) != "true" {
		t.Fatalf("quota_provider=%s", registration.Capabilities["quota_provider"])
	}
}

const webFixture = `{"limits_progress":[{"feature_name":"deep_research","remaining":250,"reset_after":"2026-10-14T04:59:17.684161+00:00"},{"feature_name":"image_gen","remaining":867,"reset_after":"2026-09-14T11:35:35.684179+00:00"}]}`

func TestQuotaSplitsWebFromCodex_whenBothPayloadsArePresent(t *testing.T) {
	view, err := quotaFromParts([]byte(usageFixture), []byte(webFixture))

	if err != nil {
		t.Fatalf("combine failed: %v", err)
	}
	names := make([]string, 0, len(view.Groups))
	for _, group := range view.Groups {
		names = append(names, group.DisplayName)
	}
	if len(view.Groups) != 2 || names[0] != "ChatGPT Web" || names[1] != "Codex" {
		t.Fatalf("groups=%v", names)
	}
	web := bucketsByDescription(view.Groups[0])
	image, ok := web["image_gen · 867 remaining"]
	if !ok {
		t.Fatalf("image_gen bucket missing: %+v", view.Groups[0].Buckets)
	}
	if image.RemainingFraction != 1 || image.Window != "web" {
		t.Fatalf("image bucket=%+v", image)
	}
	if image.ResetTime != "2026-09-14T11:35:35Z" {
		t.Fatalf("image reset=%q", image.ResetTime)
	}
	if _, ok := web["deep_research · 250 remaining"]; !ok {
		t.Fatalf("deep_research bucket missing: %+v", view.Groups[0].Buckets)
	}
}

func TestQuotaKeepsCodexOnly_whenWebPayloadIsUnavailable(t *testing.T) {
	view, err := quotaFromParts([]byte(usageFixture), nil)

	if err != nil {
		t.Fatalf("codex-only combine failed: %v", err)
	}
	if len(view.Groups) != 1 || view.Groups[0].DisplayName != "Codex" {
		t.Fatalf("groups=%+v", view.Groups)
	}
}

func TestWebQuotaMapsGpt6ProModelLimits_whenInitReportsThem(t *testing.T) {
	view, err := quotaFromParts([]byte(usageFixture), []byte(`{
		"limits_progress":[{"feature_name":"image_gen","remaining":991,"reset_after":"2026-09-17T02:03:50Z"}],
		"model_limits":[{"model_slug":"gpt-6-pro","using_default_model_slug":"gpt-6-pro","resets_after":"2026-09-17T23:31:59Z"}],
		"blocked_features":[{"name":"reason","limit":200,"resets_after":"2026-09-17T23:31:59Z","resets_after_text":"in 23 hours","description":"Capabilities reduced until tomorrow"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	web := bucketsByDescription(view.Groups[0])
	pro, ok := web["gpt-6-pro · limit 200 · reduced in 23 hours"]
	if !ok {
		t.Fatalf("gpt-6-pro bucket missing: %+v", view.Groups[0].Buckets)
	}
	if pro.RemainingFraction != 0 || pro.Window != "web" {
		t.Fatalf("pro bucket=%+v", pro)
	}
	if pro.ResetTime != "2026-09-17T23:31:59Z" {
		t.Fatalf("pro reset=%q", pro.ResetTime)
	}
}

func TestWebQuotaReportsExhaustion_whenNothingRemains(t *testing.T) {
	view, err := quotaFromParts([]byte(usageFixture), []byte(`{"limits_progress":[{"feature_name":"image_gen","remaining":0,"reset_after":"2026-09-14T11:35:35Z"}]}`))

	if err != nil {
		t.Fatal(err)
	}
	bucket := view.Groups[0].Buckets[0]
	if bucket.RemainingFraction != 0 {
		t.Fatalf("exhausted web feature must report 0, got %v", bucket.RemainingFraction)
	}
}
