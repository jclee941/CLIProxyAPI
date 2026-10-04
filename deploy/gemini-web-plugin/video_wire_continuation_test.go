package main

import (
	"encoding/json"
	"testing"
)

func TestVideoWireConversationKeepsItsVariantThroughExtensionsAndEdits(t *testing.T) {
	for _, mode := range []videoWireMode{videoWireLegacy, videoWireWeb, videoWireAlternate} {
		t.Run(string(mode), func(t *testing.T) {
			// Given a completed first turn on an eligible account.
			service, local := continuationFixture(t)
			service.config.VideoWireMode = mode
			fixture := &continuationWebFixture{video: true, capacityFlags: []int{16, 38}}
			continuationWeb(t, service, fixture)
			service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
			first := interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`))
			service.config.VideoWireMode = videoWireAlternate
			continued := `{"model":"gemini-omni-1.1-flash","input":"second","previous_interaction_id":"` + first + `"}`
			fixture.mu.Lock()
			fixture.video, fixture.answer = false, capabilityAnswer
			fixture.mu.Unlock()
			if refused := interactionCall(t, service, local, continued); refused.OK || refused.Error.Code != "gemini_web_omni:no_video_generated" {
				t.Fatalf("extension was not refused: %+v", refused.Error)
			}
			fixture.mu.Lock()
			fixture.video = true
			fixture.mu.Unlock()
			// When the refused extension is edited from the same parent.
			interactionID(t, interactionCall(t, service, local, continued))
			// Then all three submissions retain the first wire choice.
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if len(fixture.fields) != 3 || fixture.fields[1][72] != nil || fixture.fields[2][72] != float64(webRequestEdit) {
				t.Fatal("fixture did not exercise first, extension and edit")
			}
			for i := 1; i < len(fixture.fields); i++ {
				if fixture.selections[i] != fixture.selections[0] || string(jsonFixture(t, fixture.fields[i][30])) != string(jsonFixture(t, fixture.fields[0][30])) {
					t.Fatal("conversation changed wire variant")
				}
			}
			stored, err := service.sessions.read(local.Target.TokenRef)
			if err != nil {
				t.Fatal(err)
			}
			turns, err := continuationTurns(stored)
			if err != nil {
				t.Fatal(err)
			}
			firstWire := turns[continuationKey(first)].VideoWire
			if firstWire == "" {
				t.Fatal("first choice was not persisted")
			}
			for _, turn := range turns {
				if turn.VideoWire != firstWire {
					t.Fatal("choice was not inherited in durable records")
				}
			}
		})
	}
}

func TestVideoWireDoesNotChangeTextContinuations(t *testing.T) {
	// Given an eligible account and the web video profile.
	service, local := continuationFixture(t)
	service.config.VideoWireMode = videoWireWeb
	fixture := &continuationWebFixture{capacityFlags: []int{16, 38}}
	continuationWeb(t, service, fixture)
	first := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	// When a text turn uses the shared generation transport.
	continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(first.Token, "text")))
	// Then its selection header and body slot retain the legacy values.
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if got := fixture.selections[0]; got != `[1,null,null,null,"cap-flash",null,null,0,[4,5,6,8],null,null,1,null,null,1]` {
		t.Fatalf("text selection changed: %s", got)
	}
	if got := string(jsonFixture(t, fixture.fields[0][30])); got != "[4]" {
		t.Fatalf("text slot changed: %s", got)
	}
}
