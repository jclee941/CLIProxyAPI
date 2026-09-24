package main

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestMaintenanceRejectsBadRequests_beforeUpstreamOrHostLookup(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{"id":3}`, `{"id":""}`, `{"id":"unknown"}`, `{"id":"gemini-web-a.json","extra":true}`, `{"id":"gemini-web-a.json","id":"gemini-web-b.json"}`} {
		t.Run(body, func(t *testing.T) {
			service, _, _ := maintenanceFixture(t)
			var callbacks atomic.Int32
			service.host = func(string, []byte) ([]byte, error) { callbacks.Add(1); return nil, nil }
			localSidecarAll(t, service, func(http.ResponseWriter, *http.Request) { t.Error("invalid body reached HTTP") })

			response, err := service.management(t.Context(), jsonFixture(t, managementRequest{Method: "POST", Path: "/v0/management" + maintainPath, HostCallbackID: "scope-list", Body: []byte(body)}))

			if err != nil || response.StatusCode < 400 || callbacks.Load() != 0 {
				t.Fatal("invalid request had side effects")
			}
		})
	}
}

func TestMaintenanceRegistrationAdvertisesAuthenticatedRouteAndObjectConfig(t *testing.T) {
	service := newService(nil)
	registration := invoke(t, service, "plugin.register", struct{}{})
	var manifest struct {
		Metadata struct{ ConfigFields []struct{ Name, Type string } }
	}
	if !registration.OK || json.Unmarshal(registration.Result, &manifest) != nil {
		t.Fatal("registration failed")
	}
	found := false
	for _, field := range manifest.Metadata.ConfigFields {
		if field.Name == "session_dir" && field.Type == "string" {
			found = true
		}
	}
	if !found {
		t.Fatal("session directory missing from config metadata")
	}
	registered := invoke(t, service, "management.register", struct{}{})
	var management struct {
		Routes    []struct{ Method, Path string }
		Resources []struct{ Path string }
	}
	if !registered.OK || json.Unmarshal(registered.Result, &management) != nil {
		t.Fatal("management registration failed")
	}
	found = false
	for _, route := range management.Routes {
		if route.Method == "POST" && route.Path == maintainPath {
			found = true
		}
	}
	resources := map[string]bool{}
	for _, resource := range management.Resources {
		resources[resource.Path] = true
	}
	if !found || len(management.Resources) != 3 || !resources["/index"] || !resources["/extension"] || !resources["/openapi.json"] {
		t.Fatal("maintenance was not authenticated management-only")
	}
}

// The ambiguous-DTO scenario this file used to carry described a malformed
// bridge error envelope. Upkeep now speaks to Google directly, which answers an
// unauthenticated session with a plain 401 or the sign-in redirect and never
// with that envelope, so there is no ambiguity left to reject.
