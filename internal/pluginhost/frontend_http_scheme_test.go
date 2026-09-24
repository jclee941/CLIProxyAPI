package pluginhost

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestFrontendHTTPPreservesTransportScheme(t *testing.T) {
	host := newHostWithRecords(frontendHTTPRecord("scheme", 1,
		[]pluginapi.FrontendHTTPRoute{{Method: "POST", Path: "/upload/files"}},
		func(_ context.Context, request pluginapi.FrontendHTTPRequest) (pluginapi.FrontendHTTPResponse, error) {
			body, err := json.Marshal(request)
			return pluginapi.FrontendHTTPResponse{Body: body}, err
		}))
	host.RegisterFrontendHTTPRoutes(t.Context(), nil)
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			request := httptest.NewRequest("POST", scheme+"://example.test/upload/files", nil)
			request.Header.Set("X-Forwarded-Proto", "forged")
			response := httptest.NewRecorder()
			if !host.ServeFrontendHTTP(response, request, frontendCoreScope("caller")) {
				t.Fatal("route was not dispatched")
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["Scheme"] != scheme {
				t.Fatalf("transport scheme = %v, want %s", body["Scheme"], scheme)
			}
		})
	}
}
