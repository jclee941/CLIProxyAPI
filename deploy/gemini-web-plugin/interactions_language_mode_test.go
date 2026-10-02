package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// A caller picks the interface language and the capability mode of a video turn
// through generation_config. Without them the turn is sent as it always was:
// English, on the account's standard mode.

func videoRequestOptions(t *testing.T, generationConfig string) omniOptions {
	t.Helper()
	body := `{"model":"` + interactionOmniModel + `","input":"a wave"`
	if generationConfig != "" {
		body += `,"generation_config":` + generationConfig
	}
	_, payload, err := parseInteraction([]byte(body + "}"))
	if err != nil {
		t.Fatalf("%s: %v", generationConfig, err)
	}
	var content struct {
		GenerationConfig map[string]json.RawMessage `json:"generationConfig"`
	}
	if json.Unmarshal(payload, &content) != nil {
		t.Fatalf("%s: payload %s", generationConfig, payload)
	}
	options, err := parseOmniOptions(content.GenerationConfig)
	if err != nil {
		t.Fatalf("%s: %v", generationConfig, err)
	}
	return options
}

func TestInteractionLanguageAndVideoModeReachTheVideoFields(t *testing.T) {
	for config, want := range map[string]struct {
		language string
		mode     int
	}{
		"":                                 {"en", 0},
		`{"language":"en"}`:                {"en", 0},
		`{"language":"ko"}`:                {"ko", 0},
		`{"video_mode":3}`:                 {"en", 3},
		`{"language":"ko","video_mode":6}`: {"ko", 6},
		`{"thinking_level":"extended","language":"ko"}`: {"ko", 0},
	} {
		options := videoRequestOptions(t, config)
		if options.language() != want.language || options.VideoMode != want.mode {
			t.Fatalf("%s: language %q mode %d, want %q %d", config, options.language(), options.VideoMode, want.language, want.mode)
		}
		mode := options.VideoMode
		if mode == 0 {
			mode = 1
		}
		fields := webVideoFields("a wave", mode, "conversation", options.framing(), options.thinking(), options.language(), nil)
		slot, ok := fields[1].([]any)
		if !ok || len(slot) != 1 || slot[0] != want.language {
			t.Fatalf("%s: slot 1 = %#v, want [%s]", config, fields[1], want.language)
		}
		if fields[79] != mode {
			t.Fatalf("%s: slot 79 = %#v, want %d", config, fields[79], mode)
		}
	}
	if text, ok := webGenerationFields("hello", 1, 0, "conversation", nil)[1].([]any); !ok || text[0] != "en" {
		t.Fatalf("a text turn must stay in English, slot 1 = %#v", text)
	}
}

func TestInteractionRejectsAnUnknownLanguageOrMode(t *testing.T) {
	for config, code := range map[string]string{
		`{"language":"fr"}`: "omni_invalid_language",
		`{"language":"KO"}`: "omni_invalid_language",
		`{"video_mode":0}`:  "omni_invalid_video_mode",
		`{"video_mode":-1}`: "omni_invalid_video_mode",
	} {
		body := `{"model":"` + interactionOmniModel + `","input":"a wave","generation_config":` + config + `}`
		if _, _, err := parseInteraction([]byte(body)); err == nil || safeCredentialCode(err) != code {
			t.Fatalf("%s = %v, want %s", config, err, code)
		}
	}
}

func TestVideoCapabilityForTakesTheNamedModeOrRefuses(t *testing.T) {
	account := webAccount{Capabilities: []capability{
		{CapabilityID: "flash", DisplayName: "3.8 Flash", Mode: 1},
		{CapabilityID: "pro", DisplayName: "3.1 Pro", Mode: 3},
		{CapabilityID: "lite", DisplayName: "3.5 Flash-Lite", Mode: 6},
	}}
	for mode, want := range map[int]string{0: "flash", 1: "flash", 3: "pro", 6: "lite"} {
		got, err := webVideoCapabilityFor(account, mode)
		if err != nil || got.CapabilityID != want {
			t.Fatalf("mode %d = %+v, %v, want %s", mode, got, err, want)
		}
	}
	if _, err := webVideoCapabilityFor(account, 2); err == nil || safeCredentialCode(err) != "omni_video_mode_unavailable" {
		t.Fatalf("an unadvertised mode = %v, want omni_video_mode_unavailable", err)
	}
	var public *publicError
	if _, err := webVideoCapabilityFor(account, 2); !asPublicError(err, &public) || public.HTTPStatus != 400 {
		t.Fatalf("an unadvertised mode must be a 400, got %v", err)
	}
	if _, err := webVideoCapabilityFor(webAccount{}, 0); err == nil || safeCredentialCode(err) != "account_model_unavailable" {
		t.Fatalf("no standard mode = %v, want account_model_unavailable", err)
	}
}

func TestVideoTurnsCarryTheLanguageOnTheWire(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call

	for _, step := range []struct {
		body string
		want string
	}{
		{`{"model":"gemini-omni-1.1-flash","input":"first"}`, "en"},
		{`{"model":"gemini-omni-1.1-flash","input":"first","generation_config":{"language":"ko"}}`, "ko"},
	} {
		before := len(fixture.fields)
		result := interactionCall(t, service, local, step.body)
		first := interactionID(t, result)
		if len(fixture.fields) != before+1 {
			t.Fatalf("%s: %d generation requests, want one", step.body, len(fixture.fields)-before)
		}
		slot, ok := fixture.fields[before][1].([]any)
		if !ok || len(slot) != 1 || slot[0] != step.want || fixture.languages[before] != step.want {
			t.Fatalf("%s: slot 1 = %#v and hl = %q, want %s", step.body, fixture.fields[before][1], fixture.languages[before], step.want)
		}
		if step.want == "ko" {
			extended := interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"more","previous_interaction_id":"`+first+`","generation_config":{"language":"ko","video_config":{"task":"extend"}}}`)
			interactionID(t, extended)
			last := len(fixture.fields) - 1
			slot, ok := fixture.fields[last][1].([]any)
			if !ok || slot[0] != "ko" || fixture.languages[last] != "ko" {
				t.Fatalf("extension: slot 1 = %#v and hl = %q, want ko", fixture.fields[last][1], fixture.languages[last])
			}
		}
	}
}

func TestVideoModeTheAccountDoesNotAdvertiseIsRefusedBeforeAnyRequest(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call

	result := interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first","generation_config":{"video_mode":3}}`)
	if result.OK || result.Error == nil {
		t.Fatal("a mode the account does not advertise was accepted")
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(result.Error.Message), &body); err != nil || body.Error.Code != "omni_video_mode_unavailable" {
		t.Fatalf("failure = %q, want code omni_video_mode_unavailable", result.Error.Message)
	}
	if len(fixture.fields) != 0 {
		t.Fatalf("a refused mode must not reach the product, %d generation requests", len(fixture.fields))
	}
}

func TestVideoTurnDiagNamesTheLanguage(t *testing.T) {
	for _, language := range []string{"en", "ko"} {
		diag := newVideoTurnDiag("reader", "first", webAccount{}, capability{}, webFramingPortrait, 1, language)
		reason, _ := videoDiagFields(diag, "video")["reason"].(string)
		if !containsField(reason, "lang="+language) {
			t.Fatalf("reason = %q, missing lang=%s", reason, language)
		}
	}
	reason, _ := videoDiagFields(nil, "video")["reason"].(string)
	if !containsField(reason, "lang=unknown") {
		t.Fatalf("a turn with no diagnostic must say lang=unknown, got %q", reason)
	}
}

func containsField(reason, field string) bool {
	for _, part := range strings.Fields(reason) {
		if part == field {
			return true
		}
	}
	return false
}
