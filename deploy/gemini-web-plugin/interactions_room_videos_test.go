package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

func interceptRoom(t *testing.T, service *service, model, body string, headers http.Header) requestInterceptResponse {
	t.Helper()
	intercepted := invoke(t, service, "request.intercept_before", struct {
		SourceFormat, Model string
		Headers             http.Header
		Body                []byte
		Metadata            map[string]string
	}{"interactions", model, headers, []byte(body), map[string]string{"caller_scope": testCallerScope}})
	if !intercepted.OK {
		t.Fatal(intercepted.Error)
	}
	var response requestInterceptResponse
	if err := json.Unmarshal(intercepted.Result, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestANewInteractionDeclaresItsRoomSizeToTheScheduler(t *testing.T) {
	service, _ := continuationFixture(t)
	body := `{"model":"` + interactionOmniModel + `","input":"a cat","generation_config":{"room_videos":1}}`

	response := interceptRoom(t, service, interactionOmniModel, body, nil)

	if response.Headers.Get(roomVideosHeader) != "1" || !slices.Contains(response.ClearHeaders, roomVideosHeader) {
		t.Fatalf("a one-video room was not passed to the scheduler: %+v", response)
	}
}

func TestACallerCannotSizeARoomByHeader(t *testing.T) {
	service, _ := continuationFixture(t)
	forged := http.Header{roomVideosHeader: {"1"}}
	body := `{"model":"` + interactionOmniModel + `","input":"a cat"}`

	interaction := interceptRoom(t, service, interactionOmniModel, body, forged)
	chat := interceptRoom(t, service, omniModel, `{"model":"`+omniModel+`"}`, forged)

	for name, response := range map[string]requestInterceptResponse{"interaction": interaction, "chat": chat} {
		if response.Headers.Get(roomVideosHeader) != "" || !slices.Contains(response.ClearHeaders, roomVideosHeader) {
			t.Errorf("%s: a caller-supplied room size reached the scheduler: %+v", name, response)
		}
	}
}

func TestARoomSizeOutsideANewRoomIsRefused(t *testing.T) {
	for name, config := range map[string]string{
		"more than a room":  `"generation_config":{"room_videos":4}`,
		"negative":          `"generation_config":{"room_videos":-1}`,
		"on a continuation": `"previous_interaction_id":"v1_abc","generation_config":{"room_videos":1}`,
	} {
		body := `{"model":"` + interactionOmniModel + `","input":"a cat",` + config + `}`
		if _, _, err := (tuning{}).parseInteraction([]byte(body)); safeCredentialCode(err) != "interaction_room_videos_unsupported" {
			t.Errorf("%s: err = %v, want interaction_room_videos_unsupported", name, err)
		}
	}
}

func TestSchedulerOpensAOneVideoRoomWhereOneVideoFits(t *testing.T) {
	service, local := continuationFixture(t)
	other := recordFixture(t, "b")
	seedSession(t, service, other, sessionToken{encodedToken("second")})
	service.observeQuota(local.Target.ID, roomUsage(1826))
	service.observeQuota(other.ID, roomUsage(10561))
	candidates := slotCandidates(local.Target.ID, other.ID)

	if pick := service.pickRoomAccount(omniModel, candidates, roomTurns); pick.Handled {
		t.Fatalf("a three-video room opened where it cannot finish: %+v", pick)
	}
	raw := jsonFixture(t, map[string]any{
		"Provider": provider, "Model": interactionOmniModel, "Candidates": candidates,
		"Options": map[string]any{"Headers": http.Header{roomVideosHeader: {"1"}}},
	})
	pick, err := service.pickContinuation(raw)
	if err != nil || !pick.Handled || pick.AuthID != other.ID {
		t.Fatalf("a one-video room did not open where one video fits: %+v, %v", pick, err)
	}
}

func TestAnUnreadableRoomSizeHoldsAWholeRoom(t *testing.T) {
	for _, header := range []string{"", "0", "4", "two"} {
		if videos := (tuning{}).roomVideos(header); videos != roomTurns {
			t.Errorf("(tuning{}).roomVideos(%q) = %d, want %d", header, videos, roomTurns)
		}
	}
}
