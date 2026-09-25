package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenAPIDocumentIsServedWithoutAnyAssetOnDisk(t *testing.T) {
	service := newService(nil)

	response, err := service.management(t.Context(), jsonFixture(t, managementRequest{Method: "GET", Path: openapiPath}))

	if err != nil || response.StatusCode != 200 {
		t.Fatalf("the spec was not served: %v %d", err, response.StatusCode)
	}
	if got := response.Headers.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q", got)
	}

	var document struct {
		OpenAPI string                     `json:"openapi"`
		Paths   map[string]json.RawMessage `json:"paths"`
	}
	if json.Unmarshal(response.Body, &document) != nil || document.OpenAPI == "" {
		t.Fatalf("the served document is not a spec: %.200s", response.Body)
	}
	if _, found := document.Paths["/interactions"]; !found {
		t.Fatalf("the create route is undocumented: %v", document.Paths)
	}
	if !strings.Contains(string(response.Body), `"extend"`) {
		t.Fatal("the extend task is undocumented")
	}
}

// The host only routes a resource it was told about at registration, so a spec
// that is served but unregistered answers 404 to everyone who asks for it.
func TestOpenAPIResourceIsRegisteredWithTheHost(t *testing.T) {
	registered := invoke(t, newService(nil), "management.register", struct{}{})
	if !registered.OK {
		t.Fatalf("management register: %+v", registered.Error)
	}
	var registration struct {
		Resources []struct{ Path string }
	}
	if err := json.Unmarshal(registered.Result, &registration); err != nil {
		t.Fatalf("registration is unreadable: %v", err)
	}
	for _, resource := range registration.Resources {
		if resource.Path == "/openapi.json" {
			return
		}
	}
	t.Fatalf("the spec is not reachable through the host: %+v", registration.Resources)
}

func TestOpenAPIFilesRoutesUseTheCorrectServerAndScopedAuthentication(t *testing.T) {
	var document struct {
		Paths map[string]struct {
			Servers []struct{ URL string }
			Post    struct {
				Security []map[string][]string
			}
		}
	}
	if err := json.Unmarshal(openapiDocument, &document); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/files", "/files/{id}", "/files/{id}:download", "/upload/v1beta/files"} {
		if _, found := document.Paths[path]; !found {
			t.Fatalf("missing Files route %s", path)
		}
	}
	upload, found := document.Paths["/upload/v1beta/files/resumable"]
	if !found || len(upload.Servers) != 1 || upload.Servers[0].URL != "{origin}" {
		t.Fatal("upload session URL incorrectly inherits the /v1beta server prefix")
	}
	if len(upload.Post.Security) != 1 {
		t.Fatal("upload session has no operation-level authentication contract")
	}
	if _, found := upload.Post.Security[0]["uploadIdAuth"]; !found {
		t.Fatal("upload session incorrectly requires the normal API key")
	}
}
