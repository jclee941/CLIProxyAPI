package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
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

func TestOmniSubmitsOnce_whenRegistrationUsesGenericMetadataFromSavedJSON(t *testing.T) {
	for _, operation := range []string{"new-token", "update-token", "update-reference"} {
		for _, status := range []int{200, 502} {
			t.Run(operation+"/"+strconv.Itoa(status), func(t *testing.T) {
				previous := recordFixture(t, "b")
				initial, err := authFromRecord(previous)
				if err != nil {
					t.Fatal(err)
				}
				runtime := initial.Metadata
				var saved callbackRequest
				readHost := accountHost(t, []storageRecord{previous})
				service := newService(func(method string, raw []byte) ([]byte, error) {
					if method != "host.auth.save" {
						return readHost(method, raw)
					}
					if err := json.Unmarshal(raw, &saved); err != nil {
						t.Fatal(err)
					}
					runtime = genericMetadataFromSavedJSON(t, saved.JSON)
					return []byte(`{"ok":true,"result":{"name":"saved.json"}}`), nil
				})
				token := encodedToken("test-replacement-cookie")
				store := &memorySecrets{tokens: map[string]sessionToken{previous.TokenRef: {encodedToken("test-old-cookie")}}}
				service.secrets = store
				var submissions, verifications atomic.Int32
				video := `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"video/mp4","data":"dGVzdA=="}}]}}]}`
				localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
					if request.Header.Get("x-goog-api-key") != token {
						t.Error("selected replacement token was not used")
					}
					switch request.URL.Path {
					case "/v1/session/renew":
						writeFixture(t, writer, `{"token":"`+token+`"}`)
					case "/v1/account-models":
						verifications.Add(1)
						writeFixture(t, writer, `{"available":true,"models":[]}`)
					case "/v1beta/models/gemini-web-omni:generateContent":
						submissions.Add(1)
						writer.WriteHeader(status)
						writeFixture(t, writer, video)
					default:
						t.Errorf("unexpected sidecar path %s", request.URL.Path)
					}
				})
				body := struct {
					Label      string `json:"label"`
					Token      string `json:"token,omitempty"`
					TokenRef   string `json:"token_ref,omitempty"`
					ExistingID string `json:"existing_id,omitempty"`
				}{Label: "Replacement", Token: token}
				expectedRef := recordFixture(t, "a").TokenRef
				if operation != "new-token" {
					body.ExistingID = previous.ID
					expectedRef = previous.TokenRef
				}
				if operation == "update-reference" {
					body.Token, body.TokenRef = "", previous.TokenRef
					store.tokens[previous.TokenRef] = sessionToken{token}
				}

				registered := invoke(t, service, "management.handle", managementRequest{Method: "POST", Path: accountsPath, HostCallbackID: "scope-list", Body: jsonFixture(t, body)})
				var response httpResponse
				if err := json.Unmarshal(registered.Result, &response); err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != 200 || saved.HostCallbackID != "scope-list" {
					t.Fatalf("registration failed: %s", response.Body)
				}
				payload := []byte(`{"contents":[{"role":"user","parts":[{"text":"test video"}]}]}`)
				result := invoke(t, service, "executor.execute", executorRequest{AuthID: saved.Name, AuthProvider: provider, Model: omniModel, Format: "gemini", SourceFormat: "gemini", Payload: payload, OriginalRequest: payload, StorageJSON: saved.JSON, AuthMetadata: runtime})

				if submissions.Load() != 1 || verifications.Load() != 1 {
					t.Fatalf("submissions=%d verifications=%d error=%+v", submissions.Load(), verifications.Load(), result.Error)
				}
				if status == 200 {
					var generated struct{ Payload []byte }
					if !result.OK || json.Unmarshal(result.Result, &generated) != nil || string(generated.Payload) != video {
						t.Fatalf("video result lost: %+v", result.Error)
					}
				} else if result.OK || result.Error.HTTPStatus != status || !strings.HasPrefix(result.Error.Code, "gemini_web_omni:") {
					t.Fatalf("submission failure lost stop prefix: %+v", result.Error)
				}
				assertCanonicalStopPolicy(t, runtime.RequestScopedErrors)
				stored, err := service.parseStorage(saved.JSON, true)
				if err != nil || stored.Type != provider || stored.ID != saved.Name || stored.Label != body.Label || stored.TokenRef != expectedRef || stored.Disabled {
					t.Fatalf("stored identity changed: %+v, %v", stored, err)
				}
				if operation != "new-token" && stored.ID != previous.ID {
					t.Fatal("update changed account identity")
				}
				if strings.Contains(string(saved.JSON), token) || strings.Contains(string(saved.JSON), "test-replacement-cookie") {
					t.Fatal("saved auth contains a secret")
				}
			})
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
				service.secrets = &memorySecrets{tokens: map[string]sessionToken{record.TokenRef: {encodedToken("test-refresh")}}}
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
	store := &memorySecrets{tokens: make(map[string]sessionToken)}
	service.secrets = store

	result := invoke(t, service, "executor.execute", executorRequest{AuthID: auth.ID, AuthProvider: provider, Model: omniModel, Format: "gemini", SourceFormat: "gemini", StorageJSON: auth.StorageJSON, Payload: []byte(`{"contents":[{"parts":[{"text":"test video"}]}]}`)})

	if result.OK || result.Error.Code != "omni_requires_host_request_stop_policy" || len(store.reads) != 0 {
		t.Fatalf("missing runtime policy bypassed guard: %+v", result.Error)
	}
}

func TestTokenReplacementRejectsDisabledAccount_beforeSecretOrSave(t *testing.T) {
	record := recordFixture(t, "b")
	record.Disabled = true
	readHost := accountHost(t, []storageRecord{record})
	service := newService(func(method string, raw []byte) ([]byte, error) {
		if method == "host.auth.get_runtime" {
			return jsonFixture(t, envelope{OK: true, Result: jsonFixture(t, struct {
				Auth hostEntry `json:"auth"`
			}{hostEntry{ID: record.ID, Provider: provider, Disabled: true}})}), nil
		}
		return readHost(method, raw)
	})
	store := &memorySecrets{tokens: make(map[string]sessionToken)}
	service.secrets = store
	localSidecar(t, service, func(http.ResponseWriter, *http.Request) {
		t.Error("disabled account reached sidecar")
	})

	result := invoke(t, service, "management.handle", managementRequest{Method: "POST", Path: accountsPath, HostCallbackID: "scope-list", Body: jsonFixture(t, struct {
		Label, Token string
		ExistingID   string `json:"existing_id"`
	}{"Replacement", encodedToken("test-disabled"), record.ID})})

	var response httpResponse
	if !result.OK || json.Unmarshal(result.Result, &response) != nil || response.StatusCode != 409 || store.writes != 0 || len(store.reads) != 0 {
		t.Fatalf("disabled account update was not rejected: %+v", response)
	}
}
