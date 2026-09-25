//go:build abismoke

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
)

func TestCABI_whenHealthFixtureAndConcurrentRoutes_returnsSafeStatusCounter(t *testing.T) {
	plugin := healthFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != "GET" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Error("unsafe native health request")
		}
		if _, err := io.WriteString(writer, `{"healthy":true,"version":"0.9.2","accounts":{"active":2},"secret":"test-sentinel"}`); err != nil {
			t.Error(err)
		}
	})
	previous := pluginService
	pluginService = plugin
	t.Cleanup(func() { pluginService = previous })
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 20 {
				response, err := nativeFixtureCall("model.route", []byte(`{"RequestedModel":"gpt-test","AvailableProviders":["openai-compatible-chatgpt2api"]}`))
				if err != nil || !json.Valid(response) {
					t.Error("native route call failed")
				}
				cliproxyPluginShutdown()
			}
		})
	}
	workers.Wait()

	raw, err := nativeFixtureCall("management.handle", []byte(`{"Method":"GET","Path":"/v0/management/plugins/chatgpt2api/status","Headers":{"Authorization":["Bearer test-sentinel"],"Cookie":["test-sentinel"]},"Body":"test-sentinel"}`))

	if err != nil {
		t.Fatal(err)
	}
	var response envelope
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	var httpResult httpResponse
	if err := json.Unmarshal(response.Result, &httpResult); err != nil {
		t.Fatal(err)
	}
	var status statusView
	if err := json.Unmarshal(httpResult.Body, &status); err != nil {
		t.Fatal(err)
	}
	if !response.OK || httpResult.StatusCode != 200 || !status.Upstream.Healthy || status.Plugin.RouteCount != 160 || status.Upstream.Version != "0.9.2" || *status.Upstream.Accounts.Active != 2 {
		t.Fatalf("native status mismatch: %s", httpResult.Body)
	}
}
