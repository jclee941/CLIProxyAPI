package main

import (
	"encoding/json"
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
