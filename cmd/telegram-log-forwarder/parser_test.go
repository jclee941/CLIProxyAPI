package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseErrorLog_extractsSanitizedAlert_whenCPAErrorLogIsComplete(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "error-v1beta-models-gpt-sol-generateContent-64fa6fee.log")
	content := `URL: /v1beta/models/gpt-sol:generateContent?key=secret-value
Method: POST
=== REQUEST BODY ===
{"model":"gpt-5.6-sol","input":"private prompt"}

=== RESPONSE ===
Status: 400
Content-Type: application/json

{"error":{"message":"Invalid API key sk-secretvalue123456","type":"invalid_request_error"}}
`
	requireNoError(t, os.WriteFile(path, []byte(content), 0o600))

	// When
	got, err := parseErrorLog(path)

	// Then
	requireNoError(t, err)
	requireEqual(t, "/v1beta/models/gpt-sol:generateContent", got.Route)
	requireEqual(t, "gpt-5.6-sol", got.Model)
	requireEqual(t, "64fa6fee", got.RequestID)
	requireEqual(t, "Gemini-compatible", got.API)
	requireEqual(t, 400, got.Status)
	requireEqual(t, "Invalid API key sk-…", got.Message)
	if strings.Contains(got.Message, "private prompt") {
		t.Fatal("alert message leaked request content")
	}
	if strings.Contains(got.Route, "secret-value") {
		t.Fatal("alert route leaked query credentials")
	}
}

func TestParseErrorLog_rejectsIncompleteLog_whenResponseSectionMissing(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "error-incomplete.log")
	requireNoError(t, os.WriteFile(path, []byte("URL: /v1/responses\n"), 0o600))

	// When
	_, err := parseErrorLog(path)

	// Then
	if !errors.Is(err, errIncompleteLog) {
		t.Fatalf("want incomplete log error, got %v", err)
	}
}

func TestRenderTelegramMessage_escapesHTMLFields_whenAlertContainsMarkup(t *testing.T) {
	// Given
	alert := errorAlert{
		Route:   "/v1/<messages>",
		Model:   "claude&test",
		API:     "Claude-compatible",
		Status:  500,
		Message: "upstream <failed>",
		File:    "error-test.log",
	}

	// When
	got := renderTelegramMessage(alert)

	// Then
	for _, want := range []string{"/v1/&lt;messages&gt;", "claude&amp;test", "upstream &lt;failed&gt;"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered message does not contain %q", want)
		}
	}
	if strings.Contains(got, "upstream <failed>") {
		t.Fatal("rendered message contains unescaped HTML")
	}
}

func TestRenderTelegramMessage_usesOperationalTemplate_whenServerError(t *testing.T) {
	// Given
	alert := errorAlert{
		Route:          "/v1/responses",
		Model:          "gpt-5.6-sol",
		API:            "OpenAI-compatible",
		Status:         502,
		Message:        "upstream unavailable",
		File:           "error-test.log",
		Environment:    "production",
		Instance:       "cliproxy@192.168.50.114",
		DashboardURL:   "https://cliproxy.example/management.html#/logs",
		RequestID:      "64fa6fee",
		OccurredAt:     time.Date(2026, time.August, 11, 12, 7, 24, 0, time.FixedZone("KST", 9*60*60)),
		Occurrences24h: 7,
	}

	// When
	got := renderTelegramMessage(alert)

	// Then
	for _, want := range []string{
		"🔴 <b>CPA API · UPSTREAM FAILURE</b>",
		"<code>502 Bad Gateway</code>",
		"<b>7× / 24h</b>",
		"<blockquote>upstream unavailable</blockquote>",
		"<b>Request</b>",
		"<b>Runtime</b>",
		"<b>Environment</b> <code>production</code>",
		"<b>Instance</b> <code>cliproxy@192.168.50.114</code>",
		"<b>Time</b> <code>2026-08-11 12:07:24 KST</code>",
		"<b>Request ID</b> <code>64fa6fee</code>",
		`🔎 <a href="https://cliproxy.example/management.html#/logs"><b>Open request logs</b></a>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("operational template does not contain %q", want)
		}
	}
}

func TestRenderTelegramMessage_usesWarningSeverity_whenClientError(t *testing.T) {
	// Given
	alert := errorAlert{Status: 400, Message: "invalid request"}

	// When
	got := renderTelegramMessage(alert)

	// Then
	if !strings.Contains(got, "🟡 <b>CPA API · REQUEST REJECTED</b>") {
		t.Fatalf("client error template has wrong severity: %s", got)
	}
}

func TestRenderTelegramMessage_usesRateLimitSeverity_whenTooManyRequests(t *testing.T) {
	// Given
	alert := errorAlert{Status: 429, Message: "rate limited"}

	// When
	got := renderTelegramMessage(alert)

	// Then
	if !strings.Contains(got, "🟠 <b>CPA API · RATE LIMITED</b>") {
		t.Fatalf("rate-limit template has wrong severity: %s", got)
	}
}

func TestExtractRoute_removesCredentials_whenURLContainsUserInfo(t *testing.T) {
	// Given
	prefix := "URL: https://user:secret@example.com/v1/responses?api-key=secret\n"

	// When
	got := extractRoute(prefix)

	// Then
	requireEqual(t, "https://example.com/v1/responses", got)
}

func TestExtractRoute_removesCredentials_whenMalformedURLCannotBeParsed(t *testing.T) {
	// Given
	prefix := "URL: https://user:secret@example.com/%zz?api-key=secret\n"

	// When
	got := extractRoute(prefix)

	// Then
	if strings.Contains(got, "user") || strings.Contains(got, "secret") {
		t.Fatalf("malformed route leaked userinfo: %q", got)
	}
}

func TestExtractRoute_removesAuthority_whenMalformedURLIsSchemeRelative(t *testing.T) {
	// Given
	prefix := "URL: //user:secret@example.com/%zz?api-key=secret\n"

	// When
	got := extractRoute(prefix)

	// Then
	if strings.Contains(got, "user") || strings.Contains(got, "secret") || strings.Contains(got, "example.com") {
		t.Fatalf("scheme-relative route leaked authority: %q", got)
	}
}

func TestSanitizeMessage_redactsCommonCredentials_whenErrorEchoesSecrets(t *testing.T) {
	// Given
	cases := []string{
		"AIzaSyA123456789012345678901234567890",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signaturevalue",
		"x-api-key: secret-value-123",
		"access_token=secret-value-456",
		"ghp_abcdefghijklmnopqrstuvwxyz123456",
		"AKIAIOSFODNN7EXAMPLE",
	}

	// When / Then
	for _, secret := range cases {
		if got := sanitizeMessage("failed with " + secret); strings.Contains(got, secret) {
			t.Fatalf("credential was not redacted: %q", got)
		}
	}
}

func TestRenderTelegramMessage_staysWithinTelegramLimit_whenFieldsAreHuge(t *testing.T) {
	// Given
	alert := errorAlert{
		Status:       500,
		Message:      strings.Repeat("<&", 5000),
		Model:        strings.Repeat("model&", 1000),
		Route:        "/" + strings.Repeat("route&", 1000),
		Environment:  strings.Repeat("env&", 1000),
		Instance:     strings.Repeat("node&", 1000),
		RequestID:    strings.Repeat("id&", 1000),
		DashboardURL: "https://example.com/logs",
	}

	// When
	got := renderTelegramMessage(alert)

	// Then
	if len([]rune(got)) > telegramMessageLimit {
		t.Fatalf("telegram message length = %d, want <= %d", len([]rune(got)), telegramMessageLimit)
	}
}
