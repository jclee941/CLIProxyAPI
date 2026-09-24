package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesUploadOriginPreservesConfiguredPublicHTTPS(t *testing.T) {
	service := filesTestService(t, filepath.Join(t.TempDir(), "sessions"))
	newFilesDriveFixture(t).attach(service)
	service.config.ManagerOrigin = "https://cpa.example"
	response, err := service.filesRoute(t.Context(), filesHTTPRequest{
		Method: "POST", Path: "/upload/v1beta/files", Scheme: "http", Host: "cpa.example",
		CallerScope: filesTestCaller,
		Headers: http.Header{
			"X-Goog-Upload-Protocol":              {"resumable"},
			"X-Goog-Upload-Command":               {"start"},
			"X-Goog-Upload-Header-Content-Length": {"3"},
			"X-Goog-Upload-Header-Content-Type":   {"video/mp4"},
		},
		Body: []byte(`{"file":{"name":"files/proxy-upload"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if location := response.Headers.Get("X-Goog-Upload-URL"); !strings.HasPrefix(location, "https://cpa.example/") {
		t.Fatalf("public HTTPS was downgraded behind the proxy: %s", location)
	}
}
