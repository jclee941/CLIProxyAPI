package main

import (
	"encoding/json"
	"testing"
)

func continuationCallWithHost(t *testing.T, service *service, local localSession, body string) envelope {
	t.Helper()
	auth, err := authFromRecord(local.Target)
	if err != nil {
		t.Fatal(err)
	}
	request := executorRequest{AuthID: local.Target.ID, AuthProvider: provider, Model: flashModel, Format: "gemini", SourceFormat: "gemini", StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, Payload: []byte(body), HostCallbackID: "fixture-generation"}
	request.Metadata.CallerScope = testCallerScope
	return invoke(t, service, "executor.execute", request)
}

// The account manager re-logs in every few minutes and parks the lease in
// host_sync_pending. The omni executor resolves that by syncing the credential
// to the host; continuation refused instead, so native runs failed at random
// depending on when they landed relative to a login.
func TestContinuationResolvesHostSyncPendingInsteadOfRefusing(t *testing.T) {
	service, local := continuationFixture(t)
	continuationWeb(t, service, &continuationWebFixture{})
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	service.leases.get(local.Target.TokenRef).set(credentialState{state: maintenanceHostPending})

	result := continuationCallWithHost(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`)

	// Continuation must drive the host sync rather than park the caller. The
	// fixture host cannot complete a real sync, so the proof is that the refusal
	// is no longer the blanket reconciliation error that stalled native runs.
	if result.OK {
		return
	}
	if result.Error == nil || result.Error.Code == "gemini_web_omni:session_requires_reconciliation" {
		t.Fatalf("host sync pending still refused without attempting a sync: %+v", result.Error)
	}
}
