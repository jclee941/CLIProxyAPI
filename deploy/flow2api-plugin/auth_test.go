package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFlowAuthRoundtripPreservesSourceAndStopPolicy(t *testing.T) {
	service := newService(nil)
	record := storageRecord{Type: provider, ID: "flow2api-qwer941a.json", Label: "Flow qwer941a", SourceAuthID: "gemini-web-a.json"}
	result := invoke(t, service, "auth.parse", struct{ RawJSON []byte }{jsonFixture(t, record)})
	var parsed struct {
		Handled bool
		Auth    authData
	}
	if !result.OK || json.Unmarshal(result.Result, &parsed) != nil || !parsed.Handled {
		t.Fatalf("auth parse failed: %+v", result.Error)
	}
	if parsed.Auth.Provider != provider || parsed.Auth.Metadata.SourceAuthID != record.SourceAuthID || !hasRequestStopRules(parsed.Auth.Metadata.RequestScopedErrors, "flow_") {
		t.Fatal("source binding or retry stop policy was lost")
	}
	stored, err := service.parseStorage(parsed.Auth.StorageJSON, true)
	if err != nil || stored != record {
		t.Fatalf("stored binding = %+v, %v", stored, err)
	}
	if strings.Contains(string(parsed.Auth.StorageJSON), "cookie") || strings.Contains(string(parsed.Auth.StorageJSON), "token_ref") {
		t.Fatal("Flow auth owns a credential instead of a source account reference")
	}
}

func TestFlowRefusesExecutionWithoutHostStopPolicy(t *testing.T) {
	service := newService(nil)
	result := invoke(t, service, "executor.execute", executorRequest{
		AuthProvider: provider, Model: "flow-veo-3.1-fast", Format: "gemini",
		Payload: []byte(`{"contents":[{"parts":[{"text":"a paper boat"}]}]}`),
	})
	if result.OK || result.Error.Code != "flow_requires_host_request_stop_policy" {
		t.Fatalf("unprotected generation accepted: %+v", result.Error)
	}
}
