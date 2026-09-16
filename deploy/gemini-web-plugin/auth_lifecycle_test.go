package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

func genericMetadataFromSavedJSON(t *testing.T, raw []byte) authMetadata {
	t.Helper()
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatal(err)
	}
	var runtime authMetadata
	if err := json.Unmarshal(jsonFixture(t, metadata), &runtime); err != nil {
		t.Fatal(err)
	}
	return runtime
}

func assertCanonicalStopPolicy(t *testing.T, rules []stopRule) {
	t.Helper()
	if len(rules) != 300 {
		t.Fatalf("stored policy has %d rules, want 300", len(rules))
	}
	for index, rule := range rules {
		if rule.Status != 300+index || rule.Action != "stop" || len(rule.Match) != 1 || rule.Match[0] != "gemini_web_omni:" {
			t.Fatalf("noncanonical stop rule at index %d: %+v", index, rule)
		}
	}
}

func TestAuthStoragePolicySurvivesGenericRoundtrip_whenParsedOrRefreshed(t *testing.T) {
	for _, method := range []string{"auth.parse", "auth.refresh"} {
		for _, disabled := range []bool{false, true} {
			t.Run(method+"/disabled="+strconv.FormatBool(disabled), func(t *testing.T) {
				record := recordFixture(t, "b")
				record.Disabled = disabled
				service := newService(nil)
				seedSessions(t, service, map[string]sessionToken{record.TokenRef: {encodedToken("test-refresh")}})
				localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
					if request.URL.Path != "/v1/account-models" || request.Header.Get("x-goog-api-key") != encodedToken("test-refresh") {
						t.Error("refresh used a different account or route")
					}
					writeFixture(t, writer, `{"available":true,"models":[]}`)
				})
				storage := jsonFixture(t, struct {
					storageRecord
					Rules []stopRule `json:"request_scoped_errors"`
				}{record, []stopRule{{Status: 502, Match: []string{"other:"}, Action: "continue"}}})

				result := invoke(t, service, method, struct {
					AuthID, AuthProvider string
					RawJSON, StorageJSON []byte
				}{record.ID, provider, storage, storage})

				var response struct{ Auth authData }
				if !result.OK || json.Unmarshal(result.Result, &response) != nil {
					t.Fatalf("auth lifecycle failed: %+v", result.Error)
				}
				runtime := genericMetadataFromSavedJSON(t, response.Auth.StorageJSON)
				assertCanonicalStopPolicy(t, runtime.RequestScopedErrors)
				assertCanonicalStopPolicy(t, response.Auth.Metadata.RequestScopedErrors)
				stored, err := service.parseStorage(response.Auth.StorageJSON, true)
				if err != nil || stored != record || response.Auth.Disabled != disabled || runtime.TokenRef != record.TokenRef {
					t.Fatalf("auth identity/disabled state changed: %+v, %v", stored, err)
				}
				reparsed, err := authFromRecord(stored)
				if err != nil || !bytes.Equal(reparsed.StorageJSON, response.Auth.StorageJSON) {
					t.Fatal("canonical storage encoding is not deterministic")
				}
			})
		}
	}
}

func TestOmniStillRejectsMissingRuntimePolicy_whenStorageHasRules(t *testing.T) {
	service := newService(nil)
	auth, err := authFromRecord(recordFixture(t, "a"))
	if err != nil {
		t.Fatal(err)
	}

	result := invoke(t, service, "executor.execute", executorRequest{AuthID: auth.ID, AuthProvider: provider, Model: omniModel, Format: "gemini", SourceFormat: "gemini", StorageJSON: auth.StorageJSON, Payload: []byte(`{"contents":[{"parts":[{"text":"test video"}]}]}`)})

	if result.OK || result.Error.Code != "omni_requires_host_request_stop_policy" {
		t.Fatalf("missing runtime policy bypassed guard: %+v", result.Error)
	}
}
