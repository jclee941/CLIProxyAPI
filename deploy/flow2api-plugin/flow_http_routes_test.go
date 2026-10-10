package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFlowPublicResourceListsReachRegisteredHandlers(t *testing.T) {
	for _, resource := range []string{"media", "collections", "scenes", "workflows", "entities", "voices"} {
		t.Run(resource, func(t *testing.T) {
			// Given an empty project and the native route registration.
			fixture := newFlowFixture(t)
			service, _ := flowService(t, fixture)
			fixture.reply("Zzl0ze", rpcEnvelope(t, "Zzl0ze", []any{}))
			var registration struct {
				Routes []struct{ Method, Path string }
			}
			if err := json.Unmarshal(flowHTTPRegistration(), &registration); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, route := range registration.Routes {
				found = found || route.Method == "GET" && route.Path == "/v1/flow/projects/{project}/"+resource
			}
			// When an authenticated caller requests the resource list.
			response := flowHTTPTest(t, service, flowHTTPRequest{
				Method: "GET", Path: "/v1/flow/projects/" + flowTestProject + "/" + resource, CallerScope: strings.Repeat("a", 64),
			})
			// Then the registered handler reads the project and returns its empty list.
			if !found || response.StatusCode != 200 || fixture.count("Zzl0ze") != 1 {
				t.Fatalf("registered=%v status=%d body=%s", found, response.StatusCode, response.Body)
			}
		})
	}
}

func TestFlowResourceListsDoNotTreatMalformedRepliesAsEmpty(t *testing.T) {
	for _, scenario := range []struct{ path, rpc string }{
		{"/v1/flow/projects", "UpteDb"},
		{"/v1/flow/projects/" + flowTestProject + "/media", "Zzl0ze"},
	} {
		t.Run(scenario.rpc, func(t *testing.T) {
			// Given an upstream response that no longer matches its message contract.
			fixture := newFlowFixture(t)
			service, _ := flowService(t, fixture)
			fixture.reply(scenario.rpc, rpcEnvelope(t, scenario.rpc, map[string]any{"unexpected": true}))
			// When the consumer lists resources.
			response := flowHTTPTest(t, service, flowHTTPRequest{
				Method: "GET", Path: scenario.path, CallerScope: strings.Repeat("a", 64),
			})
			// Then a protocol error is returned instead of a fabricated empty account.
			if response.StatusCode != 502 {
				t.Fatalf("response=%s", response.Body)
			}
		})
	}
}

func TestFlowMissingProviderResourceIsHTTPNotFound(t *testing.T) {
	// Given Google's NOT_FOUND response for a deleted project.
	fixture := newFlowFixture(t)
	service, _ := flowService(t, fixture)
	fixture.reply("ngNC2", flowErrorEnvelope(t, []any{"wrb.fr", "ngNC2", nil, nil, nil, []any{5}, "generic"}))
	// When a consumer retrieves it.
	response := flowHTTPTest(t, service, flowHTTPRequest{
		Method: "GET", Path: "/v1/flow/projects/" + flowTestProject, CallerScope: strings.Repeat("a", 64),
	})
	// Then it is a missing resource, not an apparent gateway outage.
	if response.StatusCode != 404 {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
}
