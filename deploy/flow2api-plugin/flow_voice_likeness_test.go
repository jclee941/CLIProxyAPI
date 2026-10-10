package main

import (
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

func TestFlowVoicePreviewPreservesVoiceAndGeneratedMediaIdentifiers(t *testing.T) {
	// Given a preset voice and the native audio-generation response.
	fixture := newFlowFixture(t)
	service, _ := flowService(t, fixture)
	voiceMedia := make([]any, 11)
	voiceMedia[10] = []any{[]any{"Achernar", "fixture", true}}
	voice := []any{"achernar", 3, "Achernar", voiceMedia}
	fixture.reply("Zzl0ze", rpcEnvelope(t, "Zzl0ze", []any{nil, nil, nil, []any{voice}}))
	audio := make([]any, 11)
	audio[0], audio[1], audio[2], audio[10] = flowTestMedia, flowTestProject, flowTestOp, []any{}
	fixture.reply("no0P6", rpcEnvelope(t, "no0P6", []any{[]any{audio}}))
	// When the consumer asks for a voice preview.
	response := flowHTTPTest(t, service, flowHTTPRequest{
		Method: "POST", Path: "/v1/flow/projects/" + flowTestProject + "/voices:preview",
		CallerScope: strings.Repeat("a", 64), Body: []byte(`{"text":"fixture","voiceId":"voices/achernar"}`),
	})
	// Then it uses the preset's native voice name and returns reusable media ids.
	var media flowMediaResource
	if err := json.Unmarshal(response.Body, &media); err != nil {
		t.Fatal(err)
	}
	args := fixture.args("no0P6", 0)
	if response.StatusCode != 201 || media.ID != flowTestMedia || media.WorkflowID != flowTestOp || media.Type != "audio" ||
		jsonField(args, 0, 0, 1, 0, 0) != "Achernar" || jsonField(args, 0, 0, 2) != "gemini_v4s_tts_flow" {
		t.Fatalf("status=%d media=%+v args=%v", response.StatusCode, media, args)
	}
}

func TestFlowVoiceWAVUsesNativePCMFormat(t *testing.T) {
	// Given PCM with an incomplete trailing sample.
	pcm := []byte{1, 2, 3, 4, 5}
	// When it is packaged for playback.
	wav := flowVoiceWAV(pcm)
	// Then only whole 16-bit samples are included with the native sample rate.
	if len(wav) != 48 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" ||
		binary.LittleEndian.Uint32(wav[24:28]) != 24000 || binary.LittleEndian.Uint16(wav[22:24]) != 1 ||
		binary.LittleEndian.Uint16(wav[34:36]) != 16 || binary.LittleEndian.Uint32(wav[40:44]) != 4 {
		t.Fatalf("invalid WAV header: %x", wav)
	}
}

func TestFlowGeneratedVoiceCanBeSavedWithoutGeneratingAgain(t *testing.T) {
	// Given an existing audio preview.
	fixture := newFlowFixture(t)
	service, _ := flowService(t, fixture)
	audio := make([]any, 11)
	audio[0], audio[1], audio[2], audio[10] = flowTestMedia, flowTestProject, flowTestOp, []any{}
	fixture.reply("as29s", rpcEnvelope(t, "as29s", audio))
	fixture.reply("lt8g5", rpcEnvelope(t, "lt8g5", []any{}))
	fixture.reply("mYWVGd", rpcEnvelope(t, "mYWVGd", []any{}))
	// When the consumer saves it to the voice library.
	response := flowHTTPTest(t, service, flowHTTPRequest{
		Method: "POST", Path: "/v1/flow/projects/" + flowTestProject + "/voices/" + flowTestMedia + ":save",
		CallerScope: strings.Repeat("a", 64), Body: []byte(`{"name":"Fixture voice"}`),
	})
	// Then only visibility and the existing workflow name change.
	mediaArgs, workflowArgs := fixture.args("lt8g5", 0), fixture.args("mYWVGd", 0)
	if response.StatusCode != 200 || jsonField(mediaArgs, 0, 0) != flowTestMedia ||
		jsonField(mediaArgs, 0, 5, 9) != float64(1) ||
		jsonField(mediaArgs, 1, 0, 0) != "media.media_metadata.visibility" ||
		jsonField(workflowArgs, 0, 0) != flowTestOp || fixture.count("no0P6") != 0 {
		t.Fatalf("status=%d media=%v workflow=%v", response.StatusCode, mediaArgs, workflowArgs)
	}
}

func TestFlowLikenessRegistrationPreservesHumanVerificationStep(t *testing.T) {
	// Given Google's actual registration URL and token response shape.
	fixture := newFlowFixture(t)
	service, _ := flowService(t, fixture)
	fixture.reply("T3Zezf", rpcEnvelope(t, "T3Zezf", []any{"https://myaccount.google.com/likeness/register?token=fixture", flowTestMedia}))
	// When the API starts registration.
	response := flowHTTPTest(t, service, flowHTTPRequest{
		Method: "POST", Path: "/v1/flow/likenesses/registrations", CallerScope: strings.Repeat("a", 64),
	})
	// Then it returns the provider handoff rather than claiming an avatar exists.
	var registration struct {
		Token                    string `json:"token"`
		RequiresUserVerification bool   `json:"requiresUserVerification"`
	}
	if err := json.Unmarshal(response.Body, &registration); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 201 || registration.Token != flowTestMedia || !registration.RequiresUserVerification {
		t.Fatalf("response=%s", response.Body)
	}
}

func TestFlowLikenessRegistrationReportsNativeState(t *testing.T) {
	for code, status := range map[int]string{1: "pending", 2: "complete", 3: "failed"} {
		t.Run(status, func(t *testing.T) {
			// Given a native registration state.
			fixture := newFlowFixture(t)
			service, _ := flowService(t, fixture)
			fixture.reply("lv2lXd", rpcEnvelope(t, "lv2lXd", []any{code}))
			// When the caller reads the registration.
			response := flowHTTPTest(t, service, flowHTTPRequest{
				Method: "GET", Path: "/v1/flow/likenesses/registrations/" + flowTestMedia, CallerScope: strings.Repeat("a", 64),
			})
			// Then the state is translated without inventing successful completion.
			var body struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(response.Body, &body); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 200 || body.Status != status || jsonField(fixture.args("lv2lXd", 0), 0) != flowTestMedia {
				t.Fatalf("response=%s", response.Body)
			}
		})
	}
}

func TestFlowLikenessDeleteRequiresMembershipInConfiguredAccount(t *testing.T) {
	// Given an account with no registered likenesses.
	fixture := newFlowFixture(t)
	service, _ := flowService(t, fixture)
	fixture.reply("DTaVef", rpcEnvelope(t, "DTaVef", []any{}))
	// When a caller supplies an unrelated likeness id.
	response := flowHTTPTest(t, service, flowHTTPRequest{
		Method: "DELETE", Path: "/v1/flow/likenesses/" + flowTestMedia, CallerScope: strings.Repeat("a", 64),
	})
	// Then the delete never reaches the provider.
	if response.StatusCode != 404 || fixture.count("KaLHHf") != 0 {
		t.Fatalf("status=%d deletes=%d", response.StatusCode, fixture.count("KaLHHf"))
	}
}
