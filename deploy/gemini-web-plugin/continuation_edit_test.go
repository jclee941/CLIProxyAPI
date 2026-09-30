package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

const capabilityAnswer = "저는 단지 언어 모델일 뿐이고, 그것을 이해하고 응답하는 능력이 없기 때문에 도와드릴 수가 없습니다."

func TestARetriedTurnIsSentAsAnEditOfTheRefusedTurn(t *testing.T) {
	// Given a finished first turn and a continuation the product answered as a text model.
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	first := interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`))
	continued := `{"model":"gemini-omni-1.1-flash","input":"second","previous_interaction_id":"` + first + `"}`
	fixture.mu.Lock()
	fixture.video, fixture.answer = false, capabilityAnswer
	fixture.mu.Unlock()
	if refused := interactionCall(t, service, local, continued); refused.OK || refused.Error.Code != "gemini_web_omni:no_video_generated" {
		t.Fatalf("refused attempt: %+v", refused.Error)
	}
	fixture.mu.Lock()
	fixture.video = true
	fixture.mu.Unlock()

	// When the caller asks for the same turn again from the same interaction.
	interactionID(t, interactionCall(t, service, local, continued))

	// Then the retry is an edit addressed from the first turn with the newest context.
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 3 {
		t.Fatalf("submissions = %d, want 3", len(fixture.fields))
	}
	attempt, retry := fixture.fields[1], fixture.fields[2]
	if attempt[72] != nil {
		t.Fatalf("the first attempt from a parent was sent as an edit: %v", attempt[72])
	}
	if retry[72] != float64(webRequestEdit) {
		t.Fatalf("the retry was appended after the refused turn: kind=%v", retry[72])
	}
	if jsonField(retry[2], 0) != "c_chat" || jsonField(retry[2], 1) != "r_1" || jsonField(retry[2], 2) != "rc_1" || jsonField(retry[2], 9) != "context_2" {
		t.Fatalf("the edit is not addressed from the first turn with the newest context: %#v", retry[2])
	}
}

func TestATurnTheProductNeverAnsweredIsNotEdited(t *testing.T) {
	// Given a parent whose only other child was prepared and never answered.
	parent := []any{"c_chat", "r_1", "rc_1", nil, nil, nil, nil, nil, nil, "context_1"}
	encoded := string(jsonFixture(t, parent))
	turns := map[string]continuationTurn{
		"first":      {Conversation: "c_chat", Reply: "r_1", Metadata: encoded, Sequence: 1},
		"unanswered": {State: "prepared", Parent: encoded, Sequence: 2},
		"current":    {State: "prepared", Parent: encoded, Sequence: 3},
	}

	// When the current turn is laid out from that parent.
	_, edit, err := continuationEdit(turns, "current", parent)

	// Then it continues the conversation instead of replacing a turn.
	if err != nil || edit {
		t.Fatalf("edit=%v err=%v", edit, err)
	}
}

// omniControlCall drives the omni continuation control surface, which is the one
// way a test learns the receipt of a turn the product refused: a create that fails
// answers with an error and no interaction id.
func omniControlCall(t *testing.T, service *service, local localSession, body string) envelope {
	t.Helper()
	auth, err := authFromRecord(local.Target)
	if err != nil {
		t.Fatal(err)
	}
	request := executorRequest{AuthID: local.Target.ID, AuthProvider: provider, Model: omniModel, Format: "gemini", SourceFormat: "gemini", StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, Payload: []byte(body), HostCallbackID: "fixture-generation"}
	request.Metadata.CallerScope = testCallerScope
	return invoke(t, service, "executor.execute", request)
}

// refusedFirstTurn opens a conversation whose first turn the product answers as a
// text model, and returns the receipt of that refused turn.
func refusedFirstTurn(t *testing.T) (*service, localSession, *continuationWebFixture, string) {
	t.Helper()
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{answer: capabilityAnswer}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	token := continuationReceipt(t, omniControlCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`)).Token
	if refused := omniControlCall(t, service, local, submitContinuationBody(token, "first")); refused.OK || refused.Error.Code != "gemini_web_omni:no_video_generated" {
		t.Fatalf("first turn was not refused: ok=%v %+v", refused.OK, refused.Error)
	}
	return service, local, fixture, token
}

// newestAttempt reads the receipt of the turn most recently prepared from token.
func newestAttempt(t *testing.T, service *service, local localSession, token string) string {
	t.Helper()
	stored, err := service.localStore().read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	turns, err := continuationTurns(stored)
	if err != nil {
		t.Fatal(err)
	}
	return turns[continuationKey(token)].NextToken
}

func naming(previous, extra string) string {
	return `{"model":"gemini-omni-1.1-flash","input":"first.","previous_interaction_id":"` + previous + `"` + extra + `}`
}

func firstMessageAddress(context string) []any {
	return []any{"c_chat", "", "", nil, nil, nil, nil, nil, nil, context}
}

func TestARefusedFirstTurnIsRetriedAsAnEditOfTheFirstMessage(t *testing.T) {
	// Given a first turn the product answered as a text model.
	service, local, fixture, first := refusedFirstTurn(t)
	fixture.mu.Lock()
	fixture.video = true
	fixture.mu.Unlock()

	// When the caller names it as the previous interaction without extending.
	interactionID(t, interactionCall(t, service, local, naming(first, "")))

	// Then the retry is the web's edit of the first message: same conversation, no reply, no choice, newest context.
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 2 {
		t.Fatalf("submissions = %d, want 2", len(fixture.fields))
	}
	if fixture.fields[0][72] != nil {
		t.Fatalf("the first turn was sent as an edit: %v", fixture.fields[0][72])
	}
	retry := fixture.fields[1]
	if retry[72] != float64(webRequestEdit) {
		t.Fatalf("the retry was not sent as an edit: kind=%v", retry[72])
	}
	if !reflect.DeepEqual(retry[2], firstMessageAddress("context_1")) {
		t.Fatalf("slot 2 = %#v", retry[2])
	}
	if len(fixture.uploads) != 0 {
		t.Fatalf("the edit carried %d attachments", len(fixture.uploads))
	}
}

func TestASecondRefusalOfTheEditIsRetriedInTheSameConversation(t *testing.T) {
	// Given a first turn refused twice: the turn itself and its in-place edit.
	service, local, fixture, first := refusedFirstTurn(t)
	if refused := interactionCall(t, service, local, naming(first, "")); refused.OK || refused.Error.Code != "gemini_web_omni:no_video_generated" {
		t.Fatalf("the edit was not refused: %+v", refused.Error)
	}
	attempt := newestAttempt(t, service, local, first)
	fixture.mu.Lock()
	fixture.video = true
	fixture.mu.Unlock()

	// When the caller names the newest failed attempt.
	interactionID(t, interactionCall(t, service, local, naming(attempt, "")))

	// Then it is an edit of the same conversation's first message, with the context the refused edit returned.
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 3 {
		t.Fatalf("submissions = %d, want 3", len(fixture.fields))
	}
	retry := fixture.fields[2]
	if retry[72] != float64(webRequestEdit) || !reflect.DeepEqual(retry[2], firstMessageAddress("context_2")) {
		t.Fatalf("second retry: kind=%v slot 2=%#v", retry[72], retry[2])
	}
}

func TestAFirstTurnWithVideoIsStillAPlainFollowUp(t *testing.T) {
	// Given a first turn that produced a video.
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	first := interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`))

	// When a create names it without extending.
	interactionID(t, interactionCall(t, service, local, naming(first, "")))

	// Then the turn is appended after the first reply, not sent as an edit.
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	followUp := fixture.fields[1]
	if followUp[72] != nil || jsonField(followUp[2], 0) != "c_chat" || jsonField(followUp[2], 1) != "r_1" || jsonField(followUp[2], 2) != "rc_1" {
		t.Fatalf("follow-up: kind=%v slot 2=%#v", followUp[72], followUp[2])
	}
}

func TestExtendingAFinishedFirstTurnIsNotAnEdit(t *testing.T) {
	// Given a first turn that produced a video.
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	first := interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`))

	// When a create names it with task=extend.
	interactionID(t, interactionCall(t, service, local, naming(first, `,"generation_config":{"video_config":{"task":"extend"}}`)))

	// Then the extension continues from the first reply, not from the conversation alone.
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	extension := fixture.fields[1]
	if extension[72] != nil || jsonField(extension[2], 1) != "r_1" {
		t.Fatalf("extension: kind=%v slot 2=%#v", extension[72], extension[2])
	}
}

func TestExtendingARefusedFirstTurnIsStillRefused(t *testing.T) {
	// Given a first turn the product answered as a text model.
	service, local, fixture, first := refusedFirstTurn(t)

	// When a create names it with task=extend.
	result := interactionCall(t, service, local, naming(first, `,"generation_config":{"video_config":{"task":"extend"}}`))

	// Then nothing is submitted: a turn with no video has nothing to extend.
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if result.OK || len(fixture.fields) != 1 {
		t.Fatalf("ok=%v submissions=%d", result.OK, len(fixture.fields))
	}
}
