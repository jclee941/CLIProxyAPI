package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type videoDiagEvent struct{ State, Error, Reason string }

type videoDiagLog struct {
	mu     sync.Mutex
	events []videoDiagEvent
}

func (log *videoDiagLog) all() []videoDiagEvent {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]videoDiagEvent(nil), log.events...)
}

// fallbackFixture serves an account with 3.8 Flash and 4 Argon whose Argon
// answers video turns in Korean prose while Flash makes the video, under a
// plugin whose default video mode is Argon.
func fallbackFixture(t *testing.T) (*service, localSession, *continuationWebFixture, *videoDiagLog) {
	t.Helper()
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true, argon: true, refuseMode: 8, answer: capabilityAnswer, replyLanguage: "ko"}
	continuationWeb(t, service, fixture)
	host := (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	diag := &videoDiagLog{}
	service.host = func(method string, raw []byte) ([]byte, error) {
		if method != "host.log" {
			return host(method, raw)
		}
		var event struct{ Fields videoDiagEvent }
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Error(err)
		}
		if event.Fields.State == "video_turn_diag" {
			diag.mu.Lock()
			diag.events = append(diag.events, event.Fields)
			diag.mu.Unlock()
		}
		return []byte(`{"ok":true,"result":{}}`), nil
	}
	service.config.VideoMode = 8
	return service, local, fixture, diag
}

func assertTurnReports(t *testing.T, diag *videoDiagLog, want ...string) {
	t.Helper()
	events := diag.all()
	if len(events) != len(want) {
		t.Fatalf("video_turn_diag lines = %+v, want %d", events, len(want))
	}
	for index, line := range want {
		outcome, reason, _ := strings.Cut(line, " ")
		if events[index].Error != outcome || !strings.Contains(events[index].Reason, reason) {
			t.Fatalf("line %d = %+v, want error=%s and %q", index, events[index], outcome, reason)
		}
	}
}

func TestAnArgonProseAnswerIsMadeAgainOnFlashInPlaceOfTheRefusedFirstMessage(t *testing.T) {
	// Given an account whose default Argon mode answers video turns in prose.
	service, local, fixture, diag := fallbackFixture(t)

	// When a first turn names no video mode.
	interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`))

	// Then the turn is made once more on Flash as the web's edit of the refused first message.
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 2 {
		t.Fatalf("submissions = %d, want 2", len(fixture.fields))
	}
	first, again := fixture.fields[0], fixture.fields[1]
	if first[79] != float64(8) || first[72] != nil || !strings.Contains(fixture.selections[0], `"cap-argon"`) {
		t.Fatalf("first attempt: mode=%v kind=%v selection=%s", first[79], first[72], fixture.selections[0])
	}
	if again[79] != float64(1) || again[72] != float64(webRequestEdit) || !strings.Contains(fixture.selections[1], `"cap-flash"`) {
		t.Fatalf("fallback: mode=%v kind=%v selection=%s", again[79], again[72], fixture.selections[1])
	}
	if !reflect.DeepEqual(again[2], firstMessageAddress("context_1")) {
		t.Fatalf("fallback slot 2 = %#v", again[2])
	}
	assertTurnReports(t, diag, "no_video kind=first capability=4 Argon mode=8", "video kind=fallback capability=3.8 Flash mode=1")
}

func TestAnArgonProseAnswerToAnExtensionIsMadeAgainOnFlashFromTheSameParent(t *testing.T) {
	// Given a first turn Argon made, after which Argon answers in prose.
	service, local, fixture, diag := fallbackFixture(t)
	fixture.mu.Lock()
	fixture.refuseMode = 0
	fixture.mu.Unlock()
	first := interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`))
	fixture.mu.Lock()
	fixture.refuseMode = 8
	fixture.mu.Unlock()

	// When the next turn extends it and names no video mode.
	interactionID(t, interactionCall(t, service, local, naming(first, `,"generation_config":{"video_config":{"task":"extend"}}`)))

	// Then Flash replaces the refused extension from the first reply, with the context the refusal returned.
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 3 {
		t.Fatalf("submissions = %d, want 3", len(fixture.fields))
	}
	extension, again := fixture.fields[1], fixture.fields[2]
	if extension[79] != float64(8) || extension[72] != nil {
		t.Fatalf("extension: mode=%v kind=%v", extension[79], extension[72])
	}
	if again[79] != float64(1) || again[72] != float64(webRequestEdit) {
		t.Fatalf("fallback: mode=%v kind=%v", again[79], again[72])
	}
	if jsonField(again[2], 0) != "c_chat" || jsonField(again[2], 1) != "r_1" || jsonField(again[2], 2) != "rc_1" || jsonField(again[2], 9) != "context_2" {
		t.Fatalf("fallback slot 2 = %#v", again[2])
	}
	assertTurnReports(t, diag, "video kind=first capability=4 Argon", "no_video kind=extension capability=4 Argon", "video kind=fallback capability=3.8 Flash")
}

func TestAFallbackTheProductRefusesTooEndsTheTurn(t *testing.T) {
	// Given an account where every mode answers in prose.
	service, local, fixture, diag := fallbackFixture(t)
	fixture.mu.Lock()
	fixture.video = false
	fixture.mu.Unlock()

	// When a first turn names no video mode.
	result := interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`)

	// Then Flash is tried once and its refusal is the answer.
	if result.OK || result.Error.Code != "gemini_web_omni:no_video_generated" || !strings.Contains(result.Error.Message, capabilityAnswer) {
		t.Fatalf("result: ok=%v %+v", result.OK, result.Error)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 2 {
		t.Fatalf("submissions = %d, want 2", len(fixture.fields))
	}
	assertTurnReports(t, diag, "no_video kind=first capability=4 Argon", "no_video kind=fallback capability=3.8 Flash")
}

func TestARefusalOutsideTheFallbackIsNotMadeAgain(t *testing.T) {
	for name, setup := range map[string]struct {
		body    string
		prepare func(*service, *continuationWebFixture)
	}{
		"the request names the mode": {
			body: `{"model":"gemini-omni-1.1-flash","input":"first","generation_config":{"video_mode":8}}`,
		},
		"the product names a limit": {
			body:    `{"model":"gemini-omni-1.1-flash","input":"first"}`,
			prepare: func(_ *service, fixture *continuationWebFixture) { fixture.answer = webVideoLimitReplies[0] },
		},
		"the reply names no language": {
			body:    `{"model":"gemini-omni-1.1-flash","input":"first"}`,
			prepare: func(_ *service, fixture *continuationWebFixture) { fixture.replyLanguage = "" },
		},
		"the default is the standard mode": {
			body: `{"model":"gemini-omni-1.1-flash","input":"first"}`,
			prepare: func(service *service, fixture *continuationWebFixture) {
				service.config.VideoMode = 0
				fixture.refuseMode = 1
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Given a refusal the fallback does not cover.
			service, local, fixture, _ := fallbackFixture(t)
			if setup.prepare != nil {
				fixture.mu.Lock()
				setup.prepare(service, fixture)
				fixture.mu.Unlock()
			}

			// When the turn is submitted.
			result := interactionCall(t, service, local, setup.body)

			// Then the refusal is the answer and nothing is sent again.
			if result.OK || result.Error.Code != "gemini_web_omni:no_video_generated" {
				t.Fatalf("result: ok=%v %+v", result.OK, result.Error)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if len(fixture.fields) != 1 {
				t.Fatalf("submissions = %d, want 1", len(fixture.fields))
			}
		})
	}
}

func TestANativeVideoTurnRefusedInProseIsMadeAgainOnFlashInANewChat(t *testing.T) {
	// Given an account whose default Argon mode answers video turns in prose.
	service, local, fixture, diag := fallbackFixture(t)
	auth, err := authFromRecord(local.Target)
	if err != nil {
		t.Fatal(err)
	}
	request := executorRequest{AuthID: local.Target.ID, AuthProvider: provider, Model: omniModel, Format: "gemini", SourceFormat: "gemini", StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, Payload: []byte(`{"contents":[{"role":"user","parts":[{"text":"first"}]}]}`), HostCallbackID: "fixture-generation"}
	request.Metadata.CallerScope = testCallerScope

	// When a generateContent video turn names no video mode.
	result := invoke(t, service, "executor.execute", request)

	// Then Flash makes the video in a chat of its own.
	if !result.OK {
		t.Fatalf("native video failed: %+v", result.Error)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 2 || fixture.fields[0][79] != float64(8) || fixture.fields[1][79] != float64(1) {
		t.Fatalf("submissions = %d", len(fixture.fields))
	}
	if jsonField(fixture.fields[1][2], 0) != "" || !reflect.DeepEqual(fixture.fields[1][2], fixture.fields[0][2]) || fixture.fields[1][72] != nil {
		t.Fatalf("the native fallback was addressed into the refused chat: slot 2=%#v kind=%v", fixture.fields[1][2], fixture.fields[1][72])
	}
	assertTurnReports(t, diag, "no_video kind=first capability=4 Argon", "video kind=fallback capability=3.8 Flash")
}
