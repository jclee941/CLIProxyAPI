package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func accountHost(t *testing.T, records []storageRecord) hostCall {
	t.Helper()
	return func(method string, raw []byte) ([]byte, error) {
		var request callbackRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatal(err)
		}
		if request.HostCallbackID != "scope-list" {
			t.Fatalf("callback scope lost in %s", method)
		}
		entries := make([]hostEntry, 0, len(records))
		for _, record := range records {
			entries = append(entries, hostEntry{ID: record.ID, AuthIndex: record.ID, Name: record.ID, Provider: provider})
		}
		var result interface{}
		switch method {
		case "host.auth.list":
			entries = append(entries, hostEntry{ID: "other-provider", AuthIndex: "must-not-read", Provider: "gemini"})
			result = struct {
				Files []hostEntry `json:"files"`
			}{entries}
		case "host.auth.get_runtime":
			for _, entry := range entries {
				if entry.AuthIndex == request.AuthIndex {
					result = struct {
						Auth hostEntry `json:"auth"`
					}{entry}
				}
			}
		case "host.auth.get":
			for _, record := range records {
				if record.ID == request.AuthIndex {
					result = struct {
						JSON storageRecord `json:"json"`
					}{record}
				}
			}
		default:
			t.Fatalf("unexpected host method %s", method)
		}
		if result == nil {
			t.Fatalf("cross-provider lookup: %s", request.AuthIndex)
		}
		return jsonFixture(t, envelope{OK: true, Result: jsonFixture(t, result)}), nil
	}
}

func TestAccountsListIsolatesSessions_andReportsExpiredTruthfully(t *testing.T) {
	first, second := recordFixture(t, "a"), recordFixture(t, "b")
	service := newService(accountHost(t, []storageRecord{first, second}))
	store := &memorySecrets{tokens: map[string]sessionToken{first.TokenRef: {encodedToken("test-first")}, second.TokenRef: {encodedToken("test-second")}}}
	service.secrets = store
	localSidecar(t, service, func(writer http.ResponseWriter, request *http.Request) {
		switch request.Header.Get("x-goog-api-key") {
		case encodedToken("test-first"):
			if request.URL.Path == "/v1/usage" {
				writeFixture(t, writer, `{"tier":"PRO","tier_code":2,"metrics":[{"remaining_units":12,"usage_fraction":0.25,"reset_unix_seconds":1800000000,"window_kind":"5h","unit":"provider_compute_unit"}],"source":"GoogleWeb","estimated":false,"observed_at":1234}`)
			} else {
				writeFixture(t, writer, `{"available":true,"observed_at":1234,"models":[{"capability_id":"actual","display_name":"3.8 Flash","mode":1}]}`)
			}
		case encodedToken("test-second"):
			writer.WriteHeader(401)
			writeFixture(t, writer, `{"error":"untrusted-upstream-text"}`)
		default:
			t.Error("credential mixed or missing")
		}
	})
	result := invoke(t, service, "management.handle", managementRequest{Method: "GET", Path: accountsPath, HostCallbackID: "scope-list"})
	var response httpResponse
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Accounts []accountView `json:"accounts"`
		Provider string        `json:"provider"`
	}
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || len(body.Accounts) != 2 || body.Provider != provider {
		t.Fatalf("invalid account response: %s", response.Body)
	}
	if body.Accounts[0].Status != "ready" || len(body.Accounts[0].Models) != 2 || body.Accounts[0].Usage == nil || *body.Accounts[0].Usage.Metrics[0].RemainingUnits != 12 {
		t.Fatalf("lost measured health: %s", response.Body)
	}
	if body.Accounts[1].Status != "expired" || body.Accounts[1].Usage != nil || len(body.Accounts[1].Models) != 0 {
		t.Fatal("expired account inherited another account's data")
	}
	if len(store.reads) != 2 || store.reads[0] == store.reads[1] || strings.Contains(string(response.Body), "op://") || strings.Contains(string(response.Body), "untrusted-upstream-text") {
		t.Fatal("account isolation failed")
	}
}

func TestRefreshUsesOnlySelectedAccount(t *testing.T) {
	first, second := recordFixture(t, "a"), recordFixture(t, "b")
	service := newService(accountHost(t, []storageRecord{first, second}))
	store := &memorySecrets{tokens: map[string]sessionToken{second.TokenRef: {encodedToken("test-second")}}}
	service.secrets = store
	localSidecar(t, service, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(401)
		writeFixture(t, writer, `{}`)
	})
	result := invoke(t, service, "management.handle", managementRequest{Method: "POST", Path: refreshPath, HostCallbackID: "scope-list", Body: jsonFixture(t, struct {
		ID string `json:"id"`
	}{second.ID})})
	var response httpResponse
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	var view accountView
	if err := json.Unmarshal(response.Body, &view); err != nil {
		t.Fatal(err)
	}
	if view.ID != second.ID || view.Status != "expired" || len(store.reads) != 1 || store.reads[0] != second.TokenRef {
		t.Fatal("refresh changed accounts")
	}
}

func TestResourcePathsCannotReachManagementCallbacks(t *testing.T) {
	service := newService(func(string, []byte) ([]byte, error) {
		t.Fatal("resource request reached secret callback")
		return nil, nil
	})
	for _, path := range []string{"/v0/resource/plugins/gemini-web/accounts", "/v0/resource/plugins/gemini-web/refresh", "/index", "/plugins/gemini-web/accounts"} {
		result := invoke(t, service, "management.handle", managementRequest{Method: "POST", Path: path, Body: []byte(`{"label":"test","token":"ignored"}`)})
		var response httpResponse
		if err := json.Unmarshal(result.Result, &response); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 404 {
			t.Fatalf("unexpected resource routing: %d", response.StatusCode)
		}
	}
}
