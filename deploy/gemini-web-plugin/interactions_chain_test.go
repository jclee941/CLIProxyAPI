package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestABlockedOwnerHandsTheChainToAnotherAccount(t *testing.T) {
	service, source := continuationFixture(t)
	target := localRecordFixture(t)
	target.Target = recordFixture(t, "b")
	target.Token, target.State = encodedToken("target-cookie"), localReady
	auth, err := authFromRecord(target.Target)
	if err != nil {
		t.Fatal(err)
	}
	target.Projection = string(auth.StorageJSON)
	if err := service.sessions.write(target); err != nil {
		t.Fatal(err)
	}
	fixture := &continuationWebFixture{video: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{
		records: map[string]json.RawMessage{
			source.Target.ID: jsonFixture(t, source.Target),
			target.Target.ID: jsonFixture(t, target.Target),
		},
		service: service,
	}).call
	first := interactionID(t, interactionCall(t, service, source,
		`{"model":"gemini-omni-1.1-flash","input":"first"}`))
	headers := http.Header{continuationHeader: {first}}
	pick := func(candidates []struct{ ID, Provider string }) (continuationPick, error) {
		return service.pickContinuation(jsonFixture(t, map[string]any{
			"Provider": provider, "Model": interactionOmniModel,
			"Options":    map[string]any{"Headers": headers, "Metadata": map[string]string{"caller_scope": testCallerScope}},
			"Candidates": candidates,
		}))
	}
	both := slotCandidates(source.Target.ID, target.Target.ID)

	// Given an owner that can serve, the chain stays in its conversation.
	if choice, err := pick(both); err != nil || choice.AuthID != source.Target.ID {
		t.Fatalf("a serving owner was passed over: pick=%+v err=%v", choice, err)
	}
	// When the host does not offer the owner, another account takes the turn.
	if choice, err := pick(slotCandidates(target.Target.ID)); err != nil || !choice.Handled || choice.AuthID != target.Target.ID {
		t.Fatalf("an unoffered owner stalled the chain: pick=%+v err=%v", choice, err)
	}
	// When the owner's windows are spent, another account takes the turn.
	full := 1.0
	reset := float64(service.now().Add(time.Hour).Unix())
	service.observeQuota(source.Target.ID, &usageView{Metrics: []usageMetric{{WindowKind: "5h", UsageFraction: &full, ResetUnixSeconds: &reset}}})
	if choice, err := pick(both); err != nil || !choice.Handled || choice.AuthID != target.Target.ID {
		t.Fatalf("an exhausted owner kept the chain: pick=%+v err=%v", choice, err)
	}
	// A retrieval still lands on the owner, the only store holding its result.
	headers.Set(interactionRetrieveHeader, "true")
	if choice, err := pick(both); err != nil || choice.AuthID != source.Target.ID {
		t.Fatalf("a retrieval left the owner: pick=%+v err=%v", choice, err)
	}

	// Then the other account extends the uploaded video in a new conversation.
	extend := `{"model":"gemini-omni-1.1-flash","input":"continue","previous_interaction_id":"` + first + `","generation_config":{"video_config":{"task":"extend"}}}`
	next := interactionID(t, interactionCall(t, service, target, extend))
	stored, err := service.sessions.read(target.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	turns, err := continuationTurns(stored)
	if err != nil {
		t.Fatal(err)
	}
	if turn := turns[continuationKey(next)]; next == first || turn.Parent != "" || turn.State != "complete" || !turn.ResultStored || turn.CallerScope != testCallerScope {
		t.Fatalf("the carried turn is not an independent stored turn: %+v", turn)
	}
	// And another caller cannot carry the video.
	foreign := interactionExecutorRequest(t, target, extend)
	foreign.Metadata.CallerScope = strings.Repeat("d", 64)
	if _, err := service.executeInteraction(t.Context(), foreign); safeCredentialCode(err) != "continuation_identity_mismatch" {
		t.Fatalf("a foreign caller carried the chain: %v", err)
	}
	// And the new owner continues its own conversation without another upload.
	interactionID(t, interactionCall(t, service, target,
		`{"model":"gemini-omni-1.1-flash","input":"again","previous_interaction_id":"`+next+`","generation_config":{"video_config":{"task":"extend"}}}`))

	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 3 || len(fixture.uploads) != 1 {
		t.Fatalf("submissions=%d uploads=%d, want 3 and 1", len(fixture.fields), len(fixture.uploads))
	}
	if !bytes.Equal(fixture.uploads[0], []byte("0000ftypvideo")) || fixture.uploadCookies[0] != "target-cookie" {
		t.Fatal("the source video was not uploaded with the target account")
	}
	carried := fixture.fields[1]
	if jsonField(carried, 2, 0) != "" || jsonField(carried, 0, 3, 0, 0, 0) != "/uploaded/video" ||
		jsonField(carried, 0, 0) != uploadedVideoDeclaration+"continue" {
		t.Fatalf("the carried turn did not extend the uploaded video: prompt=%v conversation=%v", jsonField(carried, 0, 0), jsonField(carried, 2, 0))
	}
	continued := fixture.fields[2]
	if jsonField(continued, 2, 0) != "c_chat" || jsonField(continued, 0, 0) != previousVideoDeclaration+"again" {
		t.Fatalf("the new owner left its conversation: prompt=%v conversation=%v", jsonField(continued, 0, 0), jsonField(continued, 2, 0))
	}
}

// The reference the plugin writes for itself has to survive the request it is
// written into, and nothing a caller can write may be read as one.
func TestAChainedReferenceIsOnlyEverOneThePluginWrote(t *testing.T) {
	location := chainedLocation{Account: "ref-1", Key: continuationKey(strings.Repeat("a", 64)), Caller: strings.Repeat("d", 64)}

	parsed, chained := parseChainedReference(location.String())

	if !chained || parsed != location {
		t.Fatalf("parsed = %+v chained=%t, want the location it was written from", parsed, chained)
	}
	for _, supplied := range []string{
		"https://drive.google.com/file/d/1abc/view",
		"interaction:",
		"interaction:only-one-field",
		"interaction:account|key",
		"interaction:account||caller",
		"",
	} {
		if _, chained := parseChainedReference(supplied); chained {
			t.Fatalf("%q was read as a chained reference", supplied)
		}
	}
}

func TestACallerCannotCarryAChainedReference(t *testing.T) {
	supplied := `{"model":"gemini-omni-1.1-flash","input":[{"type":"text","text":"continue"},{"type":"video","uri":"interaction:account|key|` + testCallerScope + `"}]}`
	if _, _, err := parseInteraction([]byte(supplied)); safeCredentialCode(err) != "interaction_uploaded_reference_unsupported" {
		t.Fatalf("a caller-written chained reference was accepted: %v", err)
	}
	service, _ := continuationFixture(t)
	other := chainedLocation{Account: "ref-1", Key: continuationKey("previous"), Caller: strings.Repeat("d", 64)}
	if _, err := service.mediaSources(t.Context(), []webMedia{{Reference: other.String()}}, testCallerScope); safeCredentialCode(err) != "attachment_reference_unsupported" {
		t.Fatalf("another caller's video was carried: %v", err)
	}
}

// A carried chain names the video instead of carrying its bytes, goes first so
// an extension can name it <VIDEO_0>, and survives the omni validator.
func TestACarriedChainNamesTheVideoAndSurvivesValidation(t *testing.T) {
	location := chainedLocation{Account: "ref-1", Key: continuationKey(strings.Repeat("a", 64)), Caller: strings.Repeat("d", 64)}
	payload := []byte(`{"contents":[{"role":"user","parts":[{"text":"now further away"},{"inlineData":{"mimeType":"image/png","data":"aW1hZ2U="}}]}]}`)

	for _, extend := range []bool{true, false} {
		carried, err := withChainedReference(payload, location, extend)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateOmni(carried); err != nil {
			t.Fatalf("the omni route rejected its own reference: %v", err)
		}
		prompt, media, err := webContentsToPrompt(carried)
		if err != nil {
			t.Fatalf("the prompt builder rejected its own reference: %v", err)
		}
		if len(media) != 2 || media[0].Reference != location.String() {
			t.Fatalf("media = %+v, want the chained video first", media)
		}
		if declared := strings.HasPrefix(prompt, uploadedVideoDeclaration); declared != extend {
			t.Fatalf("extend=%t declared=%t prompt=%q", extend, declared, prompt)
		}
		if len(carried) > len(payload)+256 {
			t.Fatalf("carried body grew by %d bytes, want a name rather than the video", len(carried)-len(payload))
		}
	}
}

func TestLocateChainedFindsACompletedInteractionOnAnotherAccount(t *testing.T) {
	service, local := continuationFixture(t)
	other := localRecordFixture(t)
	other.Target = recordFixture(t, "b")
	otherAuth, err := authFromRecord(other.Target)
	if err != nil {
		t.Fatal(err)
	}
	other.Projection = string(otherAuth.StorageJSON)
	other.Continuations = string(jsonFixture(t, map[string]continuationTurn{
		continuationKey("previous"): {
			CallerScope:  "caller-b",
			State:        "complete",
			ResultStored: true,
		},
	}))
	if err := service.sessions.write(other); err != nil {
		t.Fatal(err)
	}
	// A disabled owner is absent from the host's account list, so the search
	// must not depend on the host at all.
	service.host = func(string, []byte) ([]byte, error) { return nil, errors.New("host unavailable") }
	currentAuth, err := authFromRecord(local.Target)
	if err != nil {
		t.Fatal(err)
	}
	request := executorRequest{
		StorageJSON:    currentAuth.StorageJSON,
		HostCallbackID: "fixture-chain",
	}
	request.Metadata.CallerScope = "caller-b"

	location, found, err := service.locateChained(request, "previous")

	if err != nil {
		t.Fatal(err)
	}
	if !found || location.Account != other.Target.TokenRef || location.Key != continuationKey("previous") || location.Caller != "caller-b" {
		t.Fatalf("location=%+v found=%t, want the other account's completed turn", location, found)
	}
}
