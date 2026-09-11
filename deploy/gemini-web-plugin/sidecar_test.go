package main

import (
	"net/http"
	"sync/atomic"
	"testing"
)

func TestOmniExposesOnlyAllowlistedSidecarErrors_whenSubmissionFails(t *testing.T) {
	cases := []struct {
		name, body, code string
		status           int
	}{
		{"unknown secret", `{"error":{"message":"video_download_failed: cookie=\"synthetic-secret\" https://example.invalid/?token=synthetic-secret"}}`, "sidecar_request_failed", 502},
		{"malformed JSON", `{"error":{"message":"missing_video_operation"}`, "sidecar_request_failed", 502},
		{"wrong message type", `{"error":{"message":["missing_video_operation"]}}`, "sidecar_request_failed", 502},
		{"missing message", `{"error":{"code":502}}`, "sidecar_request_failed", 502},
		{"old string error", `{"error":"synthetic-secret"}`, "sidecar_request_failed", 502},
		{"401 compatibility", `{"error":{"message":"unauthenticated"}}`, "sidecar_request_failed", 401},
		{"403 compatibility", `{"error":{"message":"rpc_denied"}}`, "sidecar_request_failed", 403},
		{"404 compatibility", `{"error":{"message":"model_unavailable"}}`, "sidecar_request_failed", 404},
		{"422 compatibility", `{"error":{"message":"no_video_generated"}}`, "sidecar_request_failed", 422},
		{"499 compatibility", `{"error":{"message":"caller_disconnected"}}`, "sidecar_request_failed", 499},
		{"500 status", `{"error":{"message":"invalid_response","code":401}}`, "sidecar_invalid_response", 500},
		{"503 status", `{"error":{"message":"bootstrap_failed"}}`, "sidecar_bootstrap_failed", 503},
		{"extra private data", `{"error":{"message":"missing_video_operation","detail":"cookie=synthetic-secret"},"url":"https://example.invalid/?token=synthetic-secret"}`, "sidecar_missing_video_operation", 502},
	}
	for _, kind := range []string{
		"missing_video_operation", "video_operation_mismatch", "invalid_upstream_frame",
		"missing_upstream_response", "no_video_generated", "invalid_video_download",
		"invalid_video_origin", "video_download_failed", "invalid_response", "bootstrap_failed",
		"unauthenticated", "rpc_denied", "model_unavailable", "caller_disconnected",
	} {
		cases = append(cases, struct {
			name, body, code string
			status           int
		}{kind, `{"error":{"message":"` + kind + `"}}`, "sidecar_" + kind, 502})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service := newService(nil)
			record := recordFixture(t, "a")
			auth, err := authFromRecord(record)
			if err != nil {
				t.Fatal(err)
			}
			service.secrets = &memorySecrets{tokens: map[string]sessionToken{record.TokenRef: {encodedToken("synthetic-sidecar-error")}}}
			var calls atomic.Int32
			localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/v1/session/renew" {
					writeFixture(t, writer, `{"token":"`+encodedToken("synthetic-sidecar-error")+`"}`)
					return
				}
				calls.Add(1)
				writer.WriteHeader(test.status)
				writeFixture(t, writer, test.body)
			})

			result := invoke(t, service, "executor.execute", executorRequest{AuthID: record.ID, AuthProvider: provider, Model: omniModel, Format: "gemini", SourceFormat: "gemini", Payload: []byte(`{"contents":[{"parts":[{"text":"synthetic video request"}]}]}`), StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata})

			if result.OK || result.Error == nil {
				t.Fatal("failed submission did not return an RPC error")
			}
			want := "gemini_web_omni:" + test.code
			if result.Error.Code != want || result.Error.Message != want || result.Error.HTTPStatus != test.status {
				t.Fatalf("unexpected public error: %+v", result.Error)
			}
			if calls.Load() != 1 {
				t.Fatalf("submission was retried: %d calls", calls.Load())
			}
		})
	}
}
