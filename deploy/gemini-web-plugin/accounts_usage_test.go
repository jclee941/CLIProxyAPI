package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestNativeUsageDerivesTheTierFromTheCode(t *testing.T) {
	pro := "PRO"
	for _, testCase := range []struct {
		name string
		code float64
		tier *string
	}{
		{name: "pro", code: 2, tier: &pro},
		{name: "other", code: 3},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service := newService(nil)
			localWeb(t, service, func(writer http.ResponseWriter, request *http.Request) {
				if nativeRPCID(request) == webUsageRPC {
					writeFixture(t, writer, rpcEnvelope(t, webUsageRPC, []any{testCase.code}))
					return
				}
				if !serveAccountRPC(t, writer, request) {
					t.Errorf("unexpected request %s", request.URL)
				}
			})

			usage, err := service.usage(t.Context(), recordFixture(t, "a").TokenRef, sessionToken{encodedToken("test-usage")})

			if err != nil {
				t.Fatalf("usage: %v", err)
			}
			code := int(testCase.code)
			if usage.Source != "GoogleWeb" || usage.Estimated || usage.TierCode == nil || *usage.TierCode != code || !reflect.DeepEqual(usage.Tier, testCase.tier) || len(usage.Metrics) != 0 {
				t.Fatalf("usage = %s", jsonFixture(t, usage))
			}
		})
	}
}

func TestNativeUsageRejectsAnEmptyReply(t *testing.T) {
	service := newService(nil)
	localWeb(t, service, func(writer http.ResponseWriter, request *http.Request) {
		writeFixture(t, writer, rpcEnvelope(t, webUsageRPC, []any{}))
	})

	usage, err := service.usage(t.Context(), recordFixture(t, "a").TokenRef, sessionToken{encodedToken("test-usage")})

	var public *publicError
	if !errors.As(err, &public) || public.HTTPStatus != 502 || public.Code != "usage_response_invalid" || usage != nil {
		t.Fatalf("usage = %+v, error = %v; want nil usage and 502 usage_response_invalid", usage, err)
	}
}

func TestUsageWindowsKeepAStableOrder(t *testing.T) {
	for _, order := range [][]string{{"weekly", "5h"}, {"5h", "weekly"}} {
		metrics := make([]usageMetric, 0, len(order))
		for _, window := range order {
			metrics = append(metrics, usageMetric{WindowKind: window})
		}
		orderUsageWindows(metrics)
		if metrics[0].WindowKind != "5h" || metrics[1].WindowKind != "weekly" {
			t.Fatalf("upstream order %v leaked to the dashboard: %v", order, metrics)
		}
	}
}

// Production reads usage natively, and that reader was added without the order
// the original reader had, so four of six cards listed weekly before 5h again.
// The rows below are the shape jSf9Qc returns: remaining, fraction, window
// type and the reset under slot 3, with the weekly row first.
func TestNativeUsageListsFiveHourBeforeWeeklyWithDisplayablePercent(t *testing.T) {
	service := newService(nil)
	weekly := slots(4, map[int]any{0: float64(530213), 1: 0.4520772, 2: float64(2), 3: []any{[]any{float64(1790556342)}}})
	fiveHour := slots(4, map[int]any{0: float64(33596), 1: 0.3, 2: float64(1), 3: []any{[]any{float64(1790246742)}}})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var err error
		switch {
		case strings.HasSuffix(request.URL.Path, "/app"):
			_, err = writer.Write([]byte(`{"SNlM0e":"xsrf-token","cfb2h":"build-id","FdrFJe":"session-id"}`))
		case strings.Contains(request.URL.Path, "batchexecute") && request.URL.Query().Get("rpcids") == webUsageRPC:
			_, err = writer.Write([]byte(rpcEnvelope(t, webUsageRPC, []any{float64(3), []any{weekly, fiveHour}, false})))
		case strings.Contains(request.URL.Path, "batchexecute") && request.URL.Query().Get("rpcids") == webGxuBudgetRPC:
			_, err = writer.Write([]byte(rpcEnvelope(t, webGxuBudgetRPC, videoCapBudget(1790400000))))
		default:
			t.Errorf("unexpected request %s", request.URL)
		}
		if err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	service.webOriginOverride = server.URL

	usage, err := service.nativeUsage(t.Context(), recordFixture(t, "a").TokenRef, sessionToken{encodedToken("SID=a; SAPISID=b")})

	if err != nil {
		t.Fatalf("native usage: %v", err)
	}
	if len(usage.Metrics) != 2 || usage.Metrics[0].WindowKind != "5h" || usage.Metrics[1].WindowKind != "weekly" {
		t.Fatalf("windows = %s, want 5h then weekly", jsonFixture(t, usage.Metrics))
	}
	if percent := usage.Metrics[1].UsagePercent; percent == nil || *percent != 45.21 {
		t.Fatalf("weekly percent = %v, want 45.21", percent)
	}
	if fraction := usage.Metrics[1].UsageFraction; fraction == nil || *fraction != 0.4520772 {
		t.Fatalf("the reported fraction was altered: %v", fraction)
	}
	if usage.VideoCapped == nil || !*usage.VideoCapped || usage.VideoAvailableAt == nil || *usage.VideoAvailableAt != 1790400000 {
		t.Fatalf("video budget = %v until %v, want capped until 1790400000", usage.VideoCapped, usage.VideoAvailableAt)
	}
}
