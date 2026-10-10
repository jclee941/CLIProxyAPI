package main

import (
	"net/http"
	"os"
)

const flowResourcePath = "/v0/resource/plugins/flow2api/index"

func (service *service) flowDashboard() (httpResponse, error) {
	body, err := os.ReadFile(service.dashboard)
	if err != nil {
		return httpResponse{}, failure(503, "flow_dashboard_unavailable")
	}
	return httpResponse{StatusCode: http.StatusOK, Headers: http.Header{
		"Content-Type":           {"text/html; charset=utf-8"},
		"Cache-Control":          {"no-store"},
		"X-Content-Type-Options": {"nosniff"},
		"Referrer-Policy":        {"no-referrer"},
	}, Body: body}, nil
}
