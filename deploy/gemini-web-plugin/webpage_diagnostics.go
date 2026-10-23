package main

import "fmt"

// Only structural counts and booleans leave the page; tokens, account IDs,
// HTML, URLs and cookie values must never enter a diagnostic.
func appPageFailureFields(code, page string) map[string]any {
	_, xsrf := bootstrapValue(bootstrapXSRFPattern, page)
	_, build := bootstrapValue(bootstrapBuildPattern, page)
	_, session := bootstrapValue(bootstrapSessionPattern, page)
	return map[string]any{
		"provider": provider,
		"state":    "app_page_invalid",
		"error":    code,
		"budget":   fmt.Sprintf("bytes=%d", len(page)),
		"reason": fmt.Sprintf("xsrf=%t build=%t session=%t wiz_count=%d identity=%t",
			xsrf, build, session, len(webWizAssignment.FindAllStringIndex(page, -1)),
			webAccountDigest(page) != ""),
	}
}
