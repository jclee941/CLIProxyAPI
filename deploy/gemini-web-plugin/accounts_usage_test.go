package main

import (
	"errors"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestUsagePreservesMeasurements_whenProviderReportsNullableTier(t *testing.T) {
	pro, free, opaque, empty := "PRO", "FREE", "provider.future-v9", ""
	for _, testCase := range []struct {
		name     string
		tierJSON string
		tier     *string
	}{
		{name: "null", tierJSON: `null`},
		{name: "pro", tierJSON: `"PRO"`, tier: &pro},
		{name: "non_pro", tierJSON: `"FREE"`, tier: &free},
		{name: "opaque", tierJSON: `"provider.future-v9"`, tier: &opaque},
		{name: "empty", tierJSON: `""`, tier: &empty},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service := newService(nil)
			token := sessionToken{encodedToken("test-usage")}
			body := `{"tier":` + testCase.tierJSON + `,"tier_code":17,"metrics":[{"remaining_units":12.5,"usage_fraction":0.25,"usage_percent":25,"reset_unix_seconds":1800000000,"reset_at":1900000000,"window_kind":"5h","unit":"provider_compute_unit"},{"remaining_units":null,"usage_fraction":null,"reset_at":1800000123,"window_kind":"unknown","unit":"provider_compute_unit"}],"source":"GoogleWeb","estimated":false,"observed_at":1234.5}`
			localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != "GET" || request.URL.Path != "/v1/usage" || request.Header.Get("x-goog-api-key") != token.value {
					t.Error("usage request changed route or credential")
				}
				writeFixture(t, writer, body)
			})

			usage, err := service.usage(t.Context(), recordFixture(t, "a").TokenRef, token)

			if err != nil {
				t.Fatalf("provider tier %s rejected: %v", testCase.tierJSON, err)
			}
			tierCode := 17
			remaining, fraction, percent := 12.5, 0.25, 25.0
			reset, fallbackReset := 1800000000.0, 1800000123.0
			want := &usageView{
				Tier: testCase.tier, TierCode: &tierCode, Source: "GoogleWeb", Estimated: false, ObservedAt: 1234.5,
				Metrics: []usageMetric{
					{RemainingUnits: &remaining, UsageFraction: &fraction, UsagePercent: &percent, ResetUnixSeconds: &reset, WindowKind: "5h", Unit: "provider_compute_unit"},
					{ResetUnixSeconds: &fallbackReset, WindowKind: "unknown", Unit: "provider_compute_unit"},
				},
			}
			if !reflect.DeepEqual(usage, want) {
				t.Fatalf("usage = %s, want %s", jsonFixture(t, usage), jsonFixture(t, want))
			}
		})
	}
}

func TestUsageRejectsInvalidMeasurements_whenTierIsNonPRO(t *testing.T) {
	const validBody = `{"tier":"FREE","metrics":[{"remaining_units":12,"usage_fraction":0.25,"reset_unix_seconds":1800000000,"window_kind":"5h","unit":"provider_compute_unit"}],"source":"GoogleWeb","estimated":false,"observed_at":1234}`
	for _, testCase := range []struct {
		name        string
		original    string
		replacement string
		code        string
	}{
		{"wrong_source", `"GoogleWeb"`, `"Other"`, "usage_response_invalid"},
		{"estimated", `"estimated":false`, `"estimated":true`, "usage_response_invalid"},
		{"missing_estimated", `"estimated":false,`, ``, "usage_response_invalid"},
		{"null_estimated", `"estimated":false`, `"estimated":null`, "usage_response_invalid"},
		{"unobserved", `"observed_at":1234`, `"observed_at":0`, "usage_response_invalid"},
		{"wrong_unit", `"provider_compute_unit"`, `"tokens"`, "usage_unit_invalid"},
		{"wrong_window", `"5h"`, `"daily"`, "usage_window_invalid"},
		{"numeric_tier", `"tier":"FREE"`, `"tier":17`, "usage_response_invalid"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service := newService(nil)
			body := strings.Replace(validBody, testCase.original, testCase.replacement, 1)
			localSidecar(t, service, func(writer http.ResponseWriter, _ *http.Request) {
				writeFixture(t, writer, body)
			})

			usage, err := service.usage(t.Context(), recordFixture(t, "a").TokenRef, sessionToken{encodedToken("test-usage")})

			var public *publicError
			if !errors.As(err, &public) || public.HTTPStatus != 502 || public.Code != testCase.code || usage != nil {
				t.Fatalf("usage = %+v, error = %v; want nil usage and 502 %s", usage, err, testCase.code)
			}
		})
	}
}

func TestUsageWindowsKeepAStableOrder(t *testing.T) {
	for _, order := range [][]string{{"weekly", "5h"}, {"5h", "weekly"}} {
		metrics := make([]usageMetric, 0, len(order))
		for _, window := range order {
			metrics = append(metrics, usageMetric{WindowKind: window})
		}
		sort.SliceStable(metrics, func(first, second int) bool {
			return usageWindowRank(metrics[first].WindowKind) < usageWindowRank(metrics[second].WindowKind)
		})
		if metrics[0].WindowKind != "5h" || metrics[1].WindowKind != "weekly" {
			t.Fatalf("upstream order %v leaked to the dashboard: %v", order, metrics)
		}
	}
}
