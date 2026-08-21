package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	cpaResponseLimit    = 4 << 20
	cpaAPIKeyUsagePath  = "/v0/management/api-key-usage"
	cpaUsageStatusPath  = "/v0/management/usage-statistics-enabled"
	cpaErrorBodyMaxSize = 256
)

// recentRequestBucket mirrors sdk/cliproxy/auth.RecentRequestBucket.
type recentRequestBucket struct {
	Time    string `json:"time"`
	Success int64  `json:"success"`
	Failed  int64  `json:"failed"`
}

// apiKeyUsageEntry mirrors the management api-key-usage entry payload.
type apiKeyUsageEntry struct {
	Success        int64                 `json:"success"`
	Failed         int64                 `json:"failed"`
	RecentRequests []recentRequestBucket `json:"recent_requests"`
}

// apiKeyUsage maps provider -> "base_url|api_key" -> usage counters.
type apiKeyUsage map[string]map[string]apiKeyUsageEntry

// usageStatisticsStatus mirrors the usage-statistics-enabled response body.
type usageStatisticsStatus struct {
	Enabled bool `json:"usage-statistics-enabled"`
}

// cpaAPIError reports a non-2xx CLIProxyAPI management response.
type cpaAPIError struct {
	Path   string
	Status int
	Body   string
}

func (e *cpaAPIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("CPA management %s returned status %d", e.Path, e.Status)
	}
	return fmt.Sprintf("CPA management %s returned status %d: %s", e.Path, e.Status, e.Body)
}

// cpaClient reads usage data from the CLIProxyAPI management API.
type cpaClient struct {
	client        *http.Client
	baseURL       string
	managementKey string
	timeout       time.Duration
}

func newCPAClient(client *http.Client, baseURL, managementKey string, timeout time.Duration) *cpaClient {
	return &cpaClient{client: client, baseURL: baseURL, managementKey: managementKey, timeout: timeout}
}

// FetchAPIKeyUsage returns per-provider api key usage counters.
func (c *cpaClient) FetchAPIKeyUsage(ctx context.Context) (apiKeyUsage, error) {
	usage := make(apiKeyUsage)
	if errGet := c.get(ctx, cpaAPIKeyUsagePath, &usage); errGet != nil {
		return nil, errGet
	}
	return usage, nil
}

// FetchStatus reports whether the management API is reachable and usage statistics are on.
func (c *cpaClient) FetchStatus(ctx context.Context) (usageStatisticsStatus, error) {
	var status usageStatisticsStatus
	if errGet := c.get(ctx, cpaUsageStatusPath, &status); errGet != nil {
		return usageStatisticsStatus{}, errGet
	}
	return status, nil
}

func (c *cpaClient) get(ctx context.Context, path string, out any) error {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	endpoint := strings.TrimRight(c.baseURL, "/") + path
	request, errRequest := http.NewRequestWithContext(callCtx, http.MethodGet, endpoint, nil)
	if errRequest != nil {
		return fmt.Errorf("create CPA %s request: %s", path, c.redact(errRequest.Error()))
	}
	request.Header.Set("Authorization", "Bearer "+c.managementKey)
	request.Header.Set("Accept", "application/json")

	response, errDo := c.client.Do(request)
	if errDo != nil {
		return fmt.Errorf("send CPA %s request: %s", path, c.redact(errDo.Error()))
	}
	defer func() {
		if errClose := response.Body.Close(); errClose != nil {
			log.WithError(errClose).Warn("failed to close CPA response")
		}
	}()

	body, errRead := io.ReadAll(io.LimitReader(response.Body, cpaResponseLimit))
	if errRead != nil {
		return fmt.Errorf("read CPA %s response: %s", path, c.redact(errRead.Error()))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &cpaAPIError{Path: path, Status: response.StatusCode, Body: c.redact(truncate(string(body), cpaErrorBodyMaxSize))}
	}
	if errDecode := json.Unmarshal(body, out); errDecode != nil {
		return fmt.Errorf("decode CPA %s response: %w", path, errDecode)
	}
	return nil
}

func (c *cpaClient) redact(message string) string {
	if c.managementKey == "" {
		return message
	}
	return strings.ReplaceAll(message, c.managementKey, redactedValue)
}

func truncate(value string, limit int) string {
	trimmed := strings.TrimSpace(value)
	runes := []rune(trimmed)
	if len(runes) <= limit {
		return trimmed
	}
	return string(runes[:limit]) + "..."
}
