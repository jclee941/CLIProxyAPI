package main

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"time"
)

const (
	maskedKeySuffixLength = 4
	maskedKeyPrefix       = "****"
	timestampLayout       = "2006-01-02 15:04 MST"
)

// keyStat aggregates counters for one "base_url|api_key" composite key.
type keyStat struct {
	baseURL       string
	apiKey        string
	success       int64
	failed        int64
	recentSuccess int64
	recentFailed  int64
}

// providerStat aggregates counters for one provider.
type providerStat struct {
	name          string
	keys          []keyStat
	success       int64
	failed        int64
	recentSuccess int64
	recentFailed  int64
}

// usageReport is the deterministic, render-ready view of api key usage.
type usageReport struct {
	providers     []providerStat
	success       int64
	failed        int64
	recentSuccess int64
	recentFailed  int64
	keyCount      int
}

func (r usageReport) total() int64 {
	return r.success + r.failed
}

// summarizeUsage flattens the management payload into sorted, aggregated stats.
func summarizeUsage(usage apiKeyUsage) usageReport {
	report := usageReport{providers: make([]providerStat, 0, len(usage))}
	names := make([]string, 0, len(usage))
	for name := range usage {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		entries := usage[name]
		provider := providerStat{name: name, keys: make([]keyStat, 0, len(entries))}
		composites := make([]string, 0, len(entries))
		for composite := range entries {
			composites = append(composites, composite)
		}
		sort.Strings(composites)
		for _, composite := range composites {
			entry := entries[composite]
			baseURL, apiKey := splitCompositeKey(composite)
			stat := keyStat{baseURL: baseURL, apiKey: apiKey, success: entry.Success, failed: entry.Failed}
			for _, bucket := range entry.RecentRequests {
				stat.recentSuccess += bucket.Success
				stat.recentFailed += bucket.Failed
			}
			provider.keys = append(provider.keys, stat)
			provider.success += stat.success
			provider.failed += stat.failed
			provider.recentSuccess += stat.recentSuccess
			provider.recentFailed += stat.recentFailed
		}
		report.providers = append(report.providers, provider)
		report.success += provider.success
		report.failed += provider.failed
		report.recentSuccess += provider.recentSuccess
		report.recentFailed += provider.recentFailed
		report.keyCount += len(provider.keys)
	}
	return report
}

func splitCompositeKey(composite string) (string, string) {
	parts := strings.SplitN(composite, "|", 2)
	if len(parts) != 2 {
		return "", strings.TrimSpace(composite)
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// maskAPIKey keeps at most the last four characters of a credential.
func maskAPIKey(apiKey string) string {
	trimmed := strings.TrimSpace(apiKey)
	runes := []rune(trimmed)
	if len(runes) <= maskedKeySuffixLength {
		return maskedKeyPrefix
	}
	return maskedKeyPrefix + string(runes[len(runes)-maskedKeySuffixLength:])
}

func successRate(success, failed int64) string {
	total := success + failed
	if total == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", float64(success)*100/float64(total))
}

// renderSummary renders the overall usage summary.
func renderSummary(report usageReport, now time.Time) string {
	var builder strings.Builder
	builder.WriteString("<b>CPA Usage Summary</b>\n")
	if report.keyCount == 0 {
		builder.WriteString("No api key credentials are currently tracked.\n")
	} else {
		fmt.Fprintf(&builder, "Providers: %d | API keys: %d\n", len(report.providers), report.keyCount)
		fmt.Fprintf(&builder, "Success: %d | Failed: %d | Total: %d\n", report.success, report.failed, report.total())
		fmt.Fprintf(&builder, "Success rate: %s\n", successRate(report.success, report.failed))
		fmt.Fprintf(&builder, "Recent window: %d ok / %d failed\n", report.recentSuccess, report.recentFailed)
	}
	fmt.Fprintf(&builder, "\nUpdated: %s", html.EscapeString(now.Format(timestampLayout)))
	return builder.String()
}

// renderProviders renders the per-provider and per-key breakdown.
func renderProviders(report usageReport) string {
	var builder strings.Builder
	builder.WriteString("<b>Usage by Provider</b>\n")
	if len(report.providers) == 0 {
		builder.WriteString("No providers reported usage.")
		return builder.String()
	}
	for _, provider := range report.providers {
		fmt.Fprintf(&builder, "\n<b>%s</b> - %d ok / %d failed (%s)\n",
			html.EscapeString(provider.name), provider.success, provider.failed, successRate(provider.success, provider.failed))
		for _, key := range provider.keys {
			fmt.Fprintf(&builder, "  <code>%s</code>%s - %d ok / %d failed\n",
				html.EscapeString(maskAPIKey(key.apiKey)), renderBaseURL(key.baseURL), key.success, key.failed)
		}
	}
	return strings.TrimRight(builder.String(), "\n")
}

// renderFailures renders only the credentials with failed requests.
func renderFailures(report usageReport) string {
	var builder strings.Builder
	builder.WriteString("<b>Failed Requests</b>\n")
	if report.failed == 0 {
		builder.WriteString("No failures recorded.")
		return builder.String()
	}
	fmt.Fprintf(&builder, "Total failed: %d | Recent failed: %d\n", report.failed, report.recentFailed)
	for _, provider := range report.providers {
		if provider.failed == 0 {
			continue
		}
		fmt.Fprintf(&builder, "\n<b>%s</b> - %d failed\n", html.EscapeString(provider.name), provider.failed)
		for _, key := range provider.keys {
			if key.failed == 0 {
				continue
			}
			fmt.Fprintf(&builder, "  <code>%s</code>%s - %d failed (recent %d)\n",
				html.EscapeString(maskAPIKey(key.apiKey)), renderBaseURL(key.baseURL), key.failed, key.recentFailed)
		}
	}
	return strings.TrimRight(builder.String(), "\n")
}

// renderStatus renders the management API health check result.
func renderStatus(baseURL string, status usageStatisticsStatus, now time.Time) string {
	state := "disabled"
	if status.Enabled {
		state = "enabled"
	}
	var builder strings.Builder
	builder.WriteString("<b>CPA Status</b>\n")
	fmt.Fprintf(&builder, "Endpoint: <code>%s</code>\n", html.EscapeString(baseURL))
	builder.WriteString("Management API: reachable\n")
	fmt.Fprintf(&builder, "Usage statistics: %s\n", state)
	fmt.Fprintf(&builder, "\nChecked: %s", html.EscapeString(now.Format(timestampLayout)))
	return builder.String()
}

// renderUnavailable renders a degraded message when CPA cannot be reached.
func renderUnavailable(err error) string {
	reason := "unknown error"
	if err != nil {
		reason = truncate(err.Error(), cpaErrorBodyMaxSize)
	}
	return "<b>CPA unavailable</b>\nCould not reach the CLIProxyAPI management API.\n<code>" +
		html.EscapeString(reason) + "</code>"
}

func renderUnknownAction(action string) string {
	return "<b>Unknown action</b>\n<code>" + html.EscapeString(action) + "</code>"
}

func renderBaseURL(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	return " @ " + html.EscapeString(baseURL)
}

func helpText() string {
	return "<b>CPA Usage Bot</b>\n" +
		"/usage - usage summary\n" +
		"/start - usage summary\n" +
		"/help - this help\n" +
		"Use the buttons below to switch views.\n"
}

func dailyReportHeader(now time.Time) string {
	return fmt.Sprintf("<b>Daily CPA Report</b> - %s\n", html.EscapeString(now.Format("2006-01-02")))
}
