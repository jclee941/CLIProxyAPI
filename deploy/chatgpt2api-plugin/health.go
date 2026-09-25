package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
)

const healthURL = "http://chatgpt2api:80/health?format=json"
const healthBodyLimit = 1024 * 1024

type accountCounts struct {
	Active   *int `json:"active,omitempty"`
	Total    *int `json:"total,omitempty"`
	Limited  *int `json:"limited,omitempty"`
	Abnormal *int `json:"abnormal,omitempty"`
}

type upstreamView struct {
	Healthy  bool           `json:"healthy"`
	Version  string         `json:"version,omitempty"`
	Accounts *accountCounts `json:"accounts,omitempty"`
	Error    string         `json:"error,omitempty"`
}

func newHealthClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         (&net.Dialer{}).DialContext,
			MaxIdleConns:        2,
			MaxIdleConnsPerHost: 2,
			MaxConnsPerHost:     8,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (plugin *service) health(ctx context.Context) (result upstreamView) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return upstreamView{Error: "health_unavailable"}
	}
	request.Header.Set("Accept", "application/json")
	response, err := plugin.client.Do(request)
	if err != nil {
		return upstreamView{Error: "health_unavailable"}
	}
	defer func() {
		if errClose := response.Body.Close(); errClose != nil {
			result = upstreamView{Error: "health_unavailable"}
		}
	}()
	if response.StatusCode != http.StatusOK {
		return upstreamView{Error: "health_http_error"}
	}
	raw, errRead := io.ReadAll(io.LimitReader(response.Body, healthBodyLimit+1))
	if errRead != nil {
		return upstreamView{Error: "health_unavailable"}
	}
	if len(raw) > healthBodyLimit {
		return upstreamView{Error: "health_response_too_large"}
	}
	var health struct {
		Healthy  *bool          `json:"healthy"`
		Version  string         `json:"version"`
		Accounts *accountCounts `json:"accounts"`
	}
	if json.Unmarshal(raw, &health) != nil || health.Healthy == nil {
		return upstreamView{Error: "health_invalid_response"}
	}
	if health.Accounts != nil {
		for _, count := range []*int{health.Accounts.Active, health.Accounts.Total, health.Accounts.Limited, health.Accounts.Abnormal} {
			if count != nil && *count < 0 {
				return upstreamView{Error: "health_invalid_response"}
			}
		}
	}
	return upstreamView{Healthy: *health.Healthy, Version: health.Version, Accounts: health.Accounts}
}
