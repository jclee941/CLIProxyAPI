package main

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
)

const telegramMessageLimit = 4096

func renderTelegramMessage(alert errorAlert) string {
	icon, title := alertPresentation(alert.Status)
	message := fmt.Sprintf(
		"%s <b>CPA API · %s</b>\n<code>%s</code> · <b>%d× / 24h</b>\n\n<blockquote>%s</blockquote>\n\n<b>Request</b>\n• <b>Model</b> <code>%s</code>\n• <b>API</b> %s\n• <b>Route</b> <code>%s</code>\n\n<b>Runtime</b>\n• <b>Environment</b> <code>%s</code>\n• <b>Instance</b> <code>%s</code>\n• <b>Time</b> <code>%s</code>\n• <b>Request ID</b> <code>%s</code>",
		icon,
		title,
		html.EscapeString(statusSummary(alert.Status)),
		max(1, alert.Occurrences24h),
		escapeAlertField(alert.Message, 1200),
		escapeAlertField(valueOrUnknown(alert.Model), 200),
		escapeAlertField(valueOrUnknown(alert.API), 120),
		escapeAlertField(valueOrUnknown(alert.Route), 500),
		escapeAlertField(valueOrUnknown(alert.Environment), 100),
		escapeAlertField(valueOrUnknown(alert.Instance), 200),
		escapeAlertField(alertTime(alert), 100),
		escapeAlertField(valueOrUnknown(alert.RequestID), 100),
	)
	if dashboardURL := validatedDashboardURL(alert.DashboardURL); dashboardURL != "" {
		message += fmt.Sprintf("\n\n🔎 <a href=\"%s\"><b>Open request logs</b></a>", escapeAlertField(dashboardURL, 500))
	}
	return message
}

func escapeAlertField(value string, limit int) string {
	value = sanitizeMessage(value)
	var escaped strings.Builder
	for _, char := range value {
		entity := html.EscapeString(string(char))
		if escaped.Len()+len(entity) > limit-3 {
			escaped.WriteString("…")
			break
		}
		escaped.WriteString(entity)
	}
	return escaped.String()
}

func alertPresentation(status int) (string, string) {
	switch {
	case status >= 500:
		return "🔴", "UPSTREAM FAILURE"
	case status == http.StatusTooManyRequests:
		return "🟠", "RATE LIMITED"
	default:
		return "🟡", "REQUEST REJECTED"
	}
}

func statusSummary(status int) string {
	if text := http.StatusText(status); text != "" {
		return fmt.Sprintf("%d %s", status, text)
	}
	return fmt.Sprintf("HTTP %d", status)
}

func alertTime(alert errorAlert) string {
	if alert.OccurredAt.IsZero() {
		return "unknown"
	}
	return alert.OccurredAt.Format("2006-01-02 15:04:05 MST")
}

func valueOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

func validatedDashboardURL(rawURL string) string {
	parsed, errParse := url.Parse(rawURL)
	if errParse != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return ""
	}
	parsed.User = nil
	return parsed.String()
}
