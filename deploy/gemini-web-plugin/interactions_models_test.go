package main

import (
	"encoding/json"
	"testing"
)

func TestInteractionsModelIsNotAdvertisedForLegacyVaultAccounts(t *testing.T) {
	service, _ := continuationFixture(t)
	continuationWeb(t, service, &continuationWebFixture{})
	record := recordFixture(t, "b")
	service.secrets = &memorySecrets{tokens: map[string]sessionToken{record.TokenRef: {encodedToken("legacy-fixture")}}}
	// When a mixed account pool discovers a legacy, non-durable credential.
	result := invoke(t, service, "model.for_auth", struct {
		AuthID, AuthProvider string
		StorageJSON          []byte
	}{record.ID, provider, jsonFixture(t, record)})
	// Then it cannot be selected for the local-session-only Interactions model.
	if !result.OK {
		t.Fatal(result.Error)
	}
	var response struct{ Models []modelInfo }
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Models) == 0 {
		t.Fatal("stateless models disappeared")
	}
	for _, model := range response.Models {
		if model.ID == interactionOmniModel {
			t.Fatal("legacy credential advertised stored Interactions")
		}
	}
}
