package main

import "testing"

// Model discovery reads the session, asks the account what it can do, and reads
// the session again to be sure the credential was not swapped underneath. A
// generation finishing during that call changes the session in three ways that
// have nothing to do with the credential: submitting becomes ready, the active
// turn key clears, and the cookie rotation bumps the revision. Treating that as
// a swap makes the account publish no models, which drops it from the host's
// candidates - and the account it drops is the one holding the receipt the
// caller is trying to retrieve, so the retrieval is answered with
// continuation_account_unavailable while every account is healthy.
func TestModelsSurviveATurnFinishingDuringDiscovery(t *testing.T) {
	service, local := continuationFixture(t)
	key := continuationKey("aaaa")
	pinned := local
	pinned.State, pinned.ContinuationActive = localSubmitting, key
	if err := service.saveContinuations(pinned, map[string]continuationTurn{
		key: {CallerScope: testCallerScope, Model: omniModel, State: "submitting",
			Conversation: "c_chat", Reply: "r_1", Candidate: "rc_1", StartedAt: service.now().Unix()},
	}); err != nil {
		t.Fatal(err)
	}
	fixture := &continuationWebFixture{}
	continuationWeb(t, service, fixture)
	fixture.duringModels = func() {
		// The turn completes while the capability call is in flight.
		done := pinned
		done.State, done.ContinuationActive = localReady, ""
		if err := service.saveContinuations(done, map[string]continuationTurn{
			key: {CallerScope: testCallerScope, Model: omniModel, State: "complete",
				Conversation: "c_chat", Reply: "r_1", Candidate: "rc_1", ResultStored: true},
		}); err != nil {
			t.Error(err)
		}
	}

	models, err := service.localAuthModels(t.Context(), local.Target)

	if err != nil {
		t.Fatalf("a turn finishing during discovery was read as the credential changing: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("the account published no models, so the host would drop it from its candidates")
	}
}

// The guard still has to refuse the one thing it is for: a different credential
// answering under the same record.
func TestModelsRefuseACredentialSwappedDuringDiscovery(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{}
	continuationWeb(t, service, fixture)
	fixture.duringModels = func() {
		swapped := local
		swapped.Identity.AuthUser = local.Identity.AuthUser + 1
		swapped.Token = encodedTokenUser("swapped-cookie", int(swapped.Identity.AuthUser))
		if err := service.sessions.write(swapped); err != nil {
			t.Error(err)
		}
	}

	_, err := service.localAuthModels(t.Context(), local.Target)

	if safeCredentialCode(err) != "credential_changed" {
		t.Fatalf("a swapped credential was accepted: %v", err)
	}
}

// The renewal a generation runs before it submits parks the session in
// host_sync_pending and bumps the revision. That is the account preparing to
// work, and it must not stop the account publishing what it can do.
func TestModelsSurviveARenewalDuringDiscovery(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{}
	continuationWeb(t, service, fixture)
	fixture.duringModels = func() {
		// Mirrors renewLocalSession: the record the host still holds stays in
		// Previous, Target moves to the new revision, and the session parks in
		// host_sync_pending until the host catches up.
		renewed := local
		renewed.Previous = local.Target
		renewed.Target = local.Target
		renewed.Target.SessionRevision++
		renewed.State = localHostPending
		auth, err := authFromRecord(renewed.Target)
		if err != nil {
			t.Error(err)
			return
		}
		renewed.Projection = string(auth.StorageJSON)
		if err := service.sessions.write(renewed); err != nil {
			t.Error(err)
		}
	}

	models, err := service.localAuthModels(t.Context(), local.Target)

	if err != nil {
		t.Fatalf("a renewal during discovery was read as the credential changing: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("a renewing account published no models, so the host would drop it")
	}
}
