package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// A generation stream is not the place a video is waited for. The far end holds
// the connection open while it renders and ends it on its own schedule, and this
// plugin has been observed sitting on one for ten minutes only to be handed
// nothing. Everything after the receipt is read again by the turns RPC, so once
// the stream has named the conversation and the reply there is nothing left on
// it worth the exposure.
//
// The fixture here never ends its response, which is what makes the assertion
// mean something: a submit that reads to EOF cannot return, so this test only
// passes if the read stops on the receipt.
func TestSubmitStopsReadingOnceTheStreamNamesTheTurn(t *testing.T) {
	service, local := continuationFixture(t)
	hold := make(chan struct{})
	fixture := &continuationWebFixture{video: true, holdOpen: hold}
	continuationWeb(t, service, fixture)
	// Registered after the server so it runs before the server's own close.
	t.Cleanup(func() { close(hold) })
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call

	interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`))

	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 {
		t.Fatalf("submissions: %d, want the turn read through the poll rather than resubmitted", len(fixture.fields))
	}
}

// A stream truncated after its receipt used to leave the turn pinned and the
// caller holding in_progress. The receipt is everything the plugin needs, so
// the truncation now costs nothing: the poll finds the video and the same
// request answers with it.
func TestATruncatedStreamAfterTheReceiptStillAnswersWithItsVideo(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true, interrupted: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call

	interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`))

	stored, err := service.sessions.read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != localReady || stored.ContinuationActive != "" {
		t.Fatalf("a truncation the receipt had already covered left the account pinned: state=%s active=%s", stored.State, stored.ContinuationActive)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 {
		t.Fatalf("submissions: %d, want the video read through the poll rather than resubmitted", len(fixture.fields))
	}
}

// pinnedGeneration writes the durable state a submitted turn leaves behind: the
// account holds its session and the turn names an operation recovery can still
// read. A truncated stream no longer produces that state, because a receipt is
// enough to finish through the poll, so the release behaviour is exercised
// against the state itself rather than through a submit that no longer stalls.
func pinnedGeneration(t *testing.T, service *service, local localSession) (localSession, string) {
	t.Helper()
	token := strings.Repeat("a", 64)
	key := continuationKey(token)
	local.State, local.ContinuationActive = localSubmitting, key
	if err := service.saveContinuations(local, map[string]continuationTurn{
		key: {CallerScope: testCallerScope, Model: omniModel, State: "submitting",
			Conversation: "c_chat", Reply: "r_1", Candidate: "rc_1", StartedAt: service.now().Unix()},
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := service.sessions.read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != localSubmitting || stored.ContinuationActive != key {
		t.Fatalf("the fixture did not pin the turn: state=%s active=%s", stored.State, stored.ContinuationActive)
	}
	return stored, token
}
