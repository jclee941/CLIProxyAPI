package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func resourceCall(t *testing.T, service *service) httpResponse {
	t.Helper()
	request, err := json.Marshal(managementRequest{Method: "GET", Path: resourcePath, HostCallbackID: "scope-resource"})
	if err != nil {
		t.Fatal(err)
	}
	raw := service.handle(context.Background(), "management.handle", request)
	var result envelope
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("resource call failed: %+v", result.Error)
	}
	var response httpResponse
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestResourceServesDashboard_whenAssetExists(t *testing.T) {
	asset := filepath.Join(t.TempDir(), "index.html")
	if err := os.WriteFile(asset, []byte("<!doctype html><title>ChatGPT Web</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := newService(nil)
	service.dashboard = asset

	response := resourceCall(t, service)

	if response.StatusCode != 200 {
		t.Fatalf("status=%d", response.StatusCode)
	}
	if got := response.Headers.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("content-type=%q", got)
	}
	if response.Headers.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing nosniff: %+v", response.Headers)
	}
	if string(response.Body) != "<!doctype html><title>ChatGPT Web</title>" {
		t.Fatalf("body=%q", string(response.Body))
	}
}

func TestResourceFailsActionably_whenAssetIsMissing(t *testing.T) {
	service := newService(nil)
	service.dashboard = filepath.Join(t.TempDir(), "absent.html")

	response := resourceCall(t, service)

	if response.StatusCode != 503 {
		t.Fatalf("status=%d", response.StatusCode)
	}
}

func TestManagementRegisterAdvertisesTheDashboardMenu(t *testing.T) {
	service := newService(nil)

	raw := service.handle(context.Background(), "management.register", []byte(`{}`))

	var result envelope
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	var registration struct {
		Resources []struct {
			Path string `json:"Path"`
			Menu string `json:"Menu"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(result.Result, &registration); err != nil {
		t.Fatal(err)
	}
	if len(registration.Resources) != 1 || registration.Resources[0].Path != "/index" || registration.Resources[0].Menu != "ChatGPT Web" {
		t.Fatalf("resources=%+v", registration.Resources)
	}
}
