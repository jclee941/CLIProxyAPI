// Package management - config_sync.go
//
// Implements the /api/config-sync/* endpoints consumed by
// opencode-cliproxyapi-sync (https://github.com/itsmylife44/opencode-cliproxyapi-sync).
// These endpoints let OpenCode automatically refresh its cliproxy provider
// block whenever the upstream inventory changes, without requiring a
// separate CLIProxyAPI Dashboard deployment.
//
// Route group is registered in internal/api/server_routes.go as:
//
//	apiSync := s.engine.Group("/api/config-sync")
//	apiSync.Use(AuthMiddleware(s.accessManager))
//	apiSync.GET("/version", managementHandlers.ConfigSyncVersion)
//	apiSync.GET("/bundle", managementHandlers.ConfigSyncBundle)
//
// Authentication reuses cliproxy's standard API-key middleware; clients
// pass the usual `Authorization: Bearer <api-key>` header.
package management

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

// configSyncModelLimit describes the context/output token budget for a model
// entry rendered into the opencode provider block.
type configSyncModelLimit struct {
	Context int `json:"context"`
	Output  int `json:"output"`
}

// configSyncModel describes one model entry for the opencode "cliproxy"
// provider. Reasoning is omitted from JSON when false to match the
// convention established in config/providers.jsonc.
type configSyncModel struct {
	ToolCall  bool                 `json:"tool_call"`
	Reasoning bool                 `json:"reasoning,omitempty"`
	Limit     configSyncModelLimit `json:"limit"`
}

// cliproxyModels is the authoritative set of models exposed by this cliproxy
// instance. Values mirror internal/registry/models/models.json; update this
// map when the registry changes and rebuild the server.
var cliproxyModels = map[string]configSyncModel{
	"claude-3-5-haiku-20241022":  {ToolCall: true, Limit: configSyncModelLimit{Context: 128000, Output: 8192}},
	"claude-3-7-sonnet-20250219": {ToolCall: true, Limit: configSyncModelLimit{Context: 128000, Output: 8192}},
	"claude-haiku-4-5-20251001":  {ToolCall: true, Limit: configSyncModelLimit{Context: 200000, Output: 64000}},
	"claude-opus-4-1-20250805":   {ToolCall: true, Limit: configSyncModelLimit{Context: 200000, Output: 32000}},
	"claude-opus-4-20250514":     {ToolCall: true, Limit: configSyncModelLimit{Context: 200000, Output: 32000}},
	"claude-opus-4-5-20251101":   {ToolCall: true, Limit: configSyncModelLimit{Context: 200000, Output: 64000}},
	"claude-opus-4-6":            {ToolCall: true, Limit: configSyncModelLimit{Context: 1000000, Output: 128000}},
	"claude-opus-4-6-thinking":   {ToolCall: true, Reasoning: true, Limit: configSyncModelLimit{Context: 200000, Output: 64000}},
	"claude-opus-4-7":            {ToolCall: true, Limit: configSyncModelLimit{Context: 1000000, Output: 128000}},
	"claude-sonnet-4-20250514":   {ToolCall: true, Limit: configSyncModelLimit{Context: 200000, Output: 64000}},
	"claude-sonnet-4-5-20250929": {ToolCall: true, Limit: configSyncModelLimit{Context: 200000, Output: 64000}},
	"claude-sonnet-4-6":          {ToolCall: true, Limit: configSyncModelLimit{Context: 200000, Output: 64000}},
	"gemini-3-flash":             {ToolCall: true, Limit: configSyncModelLimit{Context: 1048576, Output: 65536}},
	"gemini-3-pro-high":          {ToolCall: true, Limit: configSyncModelLimit{Context: 1048576, Output: 65535}},
	"gemini-3-pro-low":           {ToolCall: true, Limit: configSyncModelLimit{Context: 1048576, Output: 65535}},
	"gemini-3.1-flash-image":     {ToolCall: false, Limit: configSyncModelLimit{Context: 1048576, Output: 65536}},
	"gemini-3.1-flash-lite":      {ToolCall: true, Limit: configSyncModelLimit{Context: 1048576, Output: 65535}},
	"gemini-3.1-pro-high":        {ToolCall: true, Limit: configSyncModelLimit{Context: 1048576, Output: 65535}},
	"gemini-3.1-pro-low":         {ToolCall: true, Limit: configSyncModelLimit{Context: 1048576, Output: 65535}},
	"gpt-5.4":                    {ToolCall: true, Reasoning: true, Limit: configSyncModelLimit{Context: 1050000, Output: 128000}},
	"gpt-oss-120b-medium":        {ToolCall: true, Limit: configSyncModelLimit{Context: 114000, Output: 32768}},
	"kimi-k2":                    {ToolCall: true, Limit: configSyncModelLimit{Context: 131072, Output: 32768}},
	"kimi-k2-thinking":           {ToolCall: true, Reasoning: true, Limit: configSyncModelLimit{Context: 131072, Output: 32768}},
	"kimi-k2.5":                  {ToolCall: true, Limit: configSyncModelLimit{Context: 131072, Output: 32768}},
}

// configSyncVersionPayload matches `VersionResponse` in the plugin's sync.ts.
type configSyncVersionPayload struct {
	Version string `json:"version"`
}

// configSyncBundlePayload matches `Bundle` in the plugin's sync.ts.
type configSyncBundlePayload struct {
	Version          string                 `json:"version"`
	Opencode         map[string]interface{} `json:"opencode"`
	OhMyOpencode     interface{}            `json:"ohMyOpencode"`
	OhMyOpenCodeSlim interface{}            `json:"ohMyOpenCodeSlim"`
}

// computeConfigSyncVersion returns a deterministic, short sha256 hash of the
// sorted model id list. Changing any model id (add, remove, rename) flips the
// version and signals the plugin to re-download the bundle.
func computeConfigSyncVersion() string {
	ids := make([]string, 0, len(cliproxyModels))
	for id := range cliproxyModels {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	h := sha256.New()
	h.Write([]byte(strings.Join(ids, "\n")))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// deriveBaseURL returns the public base URL for the cliproxy /v1 API, derived
// from the inbound request so the opencode config always points back at
// whatever host the sync plugin used to reach us.
func deriveBaseURL(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if forwarded := c.GetHeader("X-Forwarded-Proto"); forwarded != "" {
		// Take the first value in case multiple hops appended entries.
		if idx := strings.IndexByte(forwarded, ','); idx >= 0 {
			forwarded = forwarded[:idx]
		}
		forwarded = strings.TrimSpace(forwarded)
		if forwarded != "" {
			scheme = forwarded
		}
	}
	host := c.Request.Host
	if h := c.GetHeader("X-Forwarded-Host"); h != "" {
		if idx := strings.IndexByte(h, ','); idx >= 0 {
			h = h[:idx]
		}
		if h = strings.TrimSpace(h); h != "" {
			host = h
		}
	}
	return scheme + "://" + host + "/v1"
}

// buildOpencodeConfig produces the minimal opencode.json payload that the
// sync plugin writes to ~/.config/opencode/opencode.json. It declares ONLY
// the cliproxy provider so the rest of the user's opencode.jsonc remains
// authoritative for everything else.
func buildOpencodeConfig(baseURL string) map[string]interface{} {
	return map[string]interface{}{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]interface{}{
			"cliproxy": map[string]interface{}{
				"npm": "@ai-sdk/openai-compatible",
				"env": []string{"CLIPROXY_API_KEY"},
				"options": map[string]interface{}{
					"baseURL": baseURL,
				},
				"models": cliproxyModels,
			},
		},
	}
}

// ConfigSyncVersion handles GET /api/config-sync/version.
// Returns a {version: string} payload that the plugin compares against its
// cached lastKnownVersion to decide whether to fetch the full bundle.
func ConfigSyncVersion(c *gin.Context) {
	c.JSON(http.StatusOK, configSyncVersionPayload{Version: computeConfigSyncVersion()})
}

// ConfigSyncBundle handles GET /api/config-sync/bundle.
// Returns the full sync payload including the opencode config snippet that
// the plugin writes to disk. ohMyOpencode and ohMyOpenCodeSlim are left nil
// because this cliproxy does not manage that workflow.
func ConfigSyncBundle(c *gin.Context) {
	c.JSON(http.StatusOK, configSyncBundlePayload{
		Version:          computeConfigSyncVersion(),
		Opencode:         buildOpencodeConfig(deriveBaseURL(c)),
		OhMyOpencode:     nil,
		OhMyOpenCodeSlim: nil,
	})
}
