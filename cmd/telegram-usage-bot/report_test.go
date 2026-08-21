package main

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func fixtureReport(t *testing.T) usageReport {
	t.Helper()
	var usage apiKeyUsage
	requireNoError(t, json.Unmarshal([]byte(apiKeyUsageFixture), &usage))
	return summarizeUsage(usage)
}

func TestSummarizeUsage_aggregatesProvidersAndKeys_fromFixture(t *testing.T) {
	// Given / When
	report := fixtureReport(t)

	// Then
	requireEqual(t, 2, len(report.providers))
	requireEqual(t, 3, report.keyCount)
	requireEqual(t, int64(117), report.success)
	requireEqual(t, int64(5), report.failed)
	requireEqual(t, int64(122), report.total())
	requireEqual(t, int64(8), report.recentSuccess)
	requireEqual(t, int64(3), report.recentFailed)
	requireEqual(t, "codex", report.providers[0].name)
	requireEqual(t, "gemini", report.providers[1].name)
	requireEqual(t, "https://api.openai.com", report.providers[0].keys[0].baseURL)
}

func TestRenderSummary_showsTotalsAndTimestamp_fromFixture(t *testing.T) {
	// Given
	report := fixtureReport(t)
	now := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)

	// When
	rendered := renderSummary(report, now)

	// Then
	requireContains(t, rendered, "<b>CPA Usage Summary</b>")
	requireContains(t, rendered, "Providers: 2 | API keys: 3")
	requireContains(t, rendered, "Success: 117 | Failed: 5 | Total: 122")
	requireContains(t, rendered, "Success rate: 95.9%")
	requireContains(t, rendered, "Recent window: 8 ok / 3 failed")
	requireContains(t, rendered, "2026-08-21 09:00 UTC")
}

func TestRenderProviders_masksAPIKeys_fromFixture(t *testing.T) {
	// Given
	report := fixtureReport(t)

	// When
	rendered := renderProviders(report)

	// Then
	requireContains(t, rendered, "<b>gemini</b> - 110 ok / 2 failed (98.2%)")
	requireContains(t, rendered, "<b>codex</b> - 7 ok / 3 failed (70.0%)")
	requireContains(t, rendered, "<code>****1234</code> @ https://generativelanguage.googleapis.com")
	requireContains(t, rendered, "<code>****abcd</code> - 10 ok / 0 failed")
	requireContains(t, rendered, "<code>****WXYZ</code> @ https://api.openai.com")
	requireAbsent(t, rendered, geminiPrimaryKey)
	requireAbsent(t, rendered, geminiSecondKey)
	requireAbsent(t, rendered, codexKey)
}

func TestRenderFailures_listsOnlyFailingKeys_fromFixture(t *testing.T) {
	// Given
	report := fixtureReport(t)

	// When
	rendered := renderFailures(report)

	// Then
	requireContains(t, rendered, "Total failed: 5 | Recent failed: 3")
	requireContains(t, rendered, "<b>codex</b> - 3 failed")
	requireContains(t, rendered, "<code>****WXYZ</code> @ https://api.openai.com - 3 failed (recent 2)")
	requireAbsent(t, rendered, "****abcd")
	requireAbsent(t, rendered, codexKey)
}

func TestRenderFailures_reportsCleanState_whenNoFailuresExist(t *testing.T) {
	// Given
	report := summarizeUsage(apiKeyUsage{
		"gemini": {"|key-abcd": {Success: 3}},
	})

	// When
	rendered := renderFailures(report)

	// Then
	requireContains(t, rendered, "No failures recorded.")
}

func TestRenderSummary_reportsEmptyState_whenNoCredentialsTracked(t *testing.T) {
	// Given
	report := summarizeUsage(apiKeyUsage{})
	now := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)

	// When
	rendered := renderSummary(report, now)

	// Then
	requireContains(t, rendered, "No api key credentials are currently tracked.")
}

func TestRenderStatus_reportsReachableEndpoint(t *testing.T) {
	// Given
	now := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)

	// When
	rendered := renderStatus("http://127.0.0.1:8317", usageStatisticsStatus{Enabled: true}, now)

	// Then
	requireContains(t, rendered, "<code>http://127.0.0.1:8317</code>")
	requireContains(t, rendered, "Management API: reachable")
	requireContains(t, rendered, "Usage statistics: enabled")
}

func TestRenderUnavailable_escapesReason(t *testing.T) {
	// Given
	err := errors.New("dial tcp <127.0.0.1:8317>: refused")

	// When
	rendered := renderUnavailable(err)

	// Then
	requireContains(t, rendered, "<b>CPA unavailable</b>")
	requireContains(t, rendered, "dial tcp &lt;127.0.0.1:8317&gt;: refused")
}

func TestMaskAPIKey_keepsAtMostLastFourCharacters(t *testing.T) {
	// Given
	cases := map[string]string{
		"AIzaSyEXAMPLEKEY1234": "****1234",
		"sk-codexkeyWXYZ":      "****WXYZ",
		"abcd":                 "****",
		"ab":                   "****",
		"":                     "****",
	}

	for input, want := range cases {
		// When
		got := maskAPIKey(input)

		// Then
		requireEqual(t, want, got)
	}
}

func TestSplitCompositeKey_separatesBaseURLFromAPIKey(t *testing.T) {
	// Given / When
	baseURL, apiKey := splitCompositeKey("https://api.openai.com|sk-secret")
	emptyBase, onlyKey := splitCompositeKey("|sk-secret")
	legacyBase, legacyKey := splitCompositeKey("sk-secret")

	// Then
	requireEqual(t, "https://api.openai.com", baseURL)
	requireEqual(t, "sk-secret", apiKey)
	requireEqual(t, "", emptyBase)
	requireEqual(t, "sk-secret", onlyKey)
	requireEqual(t, "", legacyBase)
	requireEqual(t, "sk-secret", legacyKey)
}
