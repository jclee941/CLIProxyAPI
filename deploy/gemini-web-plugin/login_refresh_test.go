package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestLoginRefreshOnlyPreservesInterruptedOperation(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary relink refused", true: "credential refreshed"}[refresh], func(t *testing.T) {
			service, host := loginFixture(t)
			started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
			initial := completeFixture(t, service, started)
			record, err := service.parseStorage(host.records[initial.AccountID], true)
			if err != nil {
				t.Fatal(err)
			}
			before, err := service.localStore().read(record.TokenRef)
			if err != nil {
				t.Fatal(err)
			}
			before.State = localSubmitting
			before.ContinuationActive = strings.Repeat("b", 64)
			before.Continuations = `{"saved_receipt":{"state":"submitting","model":"gemini-web-omni"}}`
			if err := service.localStore().write(before); err != nil {
				t.Fatal(err)
			}
			lease := service.leases.get(record.TokenRef)
			lease.set(credentialState{state: maintenanceOperator})
			relink, status := loginCall(t, service, "start", jsonFixture(t, map[string]any{"label": "Fixture", "existing_id": record.ID, "consent": true}))
			if status != 200 {
				t.Fatal("could not start same-account handoff")
			}
			user := uint64(2)
			token := encodedToken("SID=fresh")

			result, status := loginCall(t, service, "complete", jsonFixture(t, loginCompletion{
				State: relink.State, Token: token, AccountSHA256: testAccountDigest,
				AuthUser: &user, ExtensionID: strings.Repeat("a", 32), Consent: true, RefreshOnly: refresh,
			}))

			if status != 200 {
				t.Fatalf("handoff HTTP status = %d", status)
			}
			after, err := service.localStore().read(record.TokenRef)
			if err != nil {
				t.Fatal(err)
			}
			expected := before
			if refresh {
				if result.Status != loginSaved || result.ModelsReady {
					t.Fatalf("refresh status = %+v", result)
				}
				expected.Token = token
				expected.LoginState, expected.LoginExpires = relink.State, relink.ExpiresAt
				expected.LoginTokenHash = tokenFingerprint(sessionToken{token})
			} else if result.Error != "session_requires_reconciliation" {
				t.Fatalf("ordinary relink unexpectedly accepted: %+v", result)
			}
			if !reflect.DeepEqual(after, expected) || host.saves != 1 || lease.snapshot().state != maintenanceOperator {
				t.Fatal("refresh changed the existing operation, revision, host projection or fence")
			}
		})
	}
}

func TestLoginRefreshOnlyRequiresExistingAccount(t *testing.T) {
	service, host := loginFixture(t)
	started, _ := loginCall(t, service, "start", []byte(`{"label":"Fixture","consent":true}`))
	user := uint64(2)
	result, status := loginCall(t, service, "complete", jsonFixture(t, loginCompletion{
		State: started.State, Token: encodedToken("SID=fresh"), AccountSHA256: testAccountDigest,
		AuthUser: &user, ExtensionID: strings.Repeat("a", 32), Consent: true, RefreshOnly: true,
	}))
	if status != 200 || result.Error != "credential_refresh_requires_existing_account" || host.saves != 0 {
		t.Fatalf("refresh created a new account: %+v, saves=%d", result, host.saves)
	}
}
