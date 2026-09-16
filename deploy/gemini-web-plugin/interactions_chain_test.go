package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestReferenceChainRoutesAndUploadsOnAnotherAccount(t *testing.T) {
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
	body := `{"model":"gemini-omni-1.1-flash","input":"use the previous video","previous_interaction_id":"` + first + `"}`
	headers := http.Header{continuationHeader: {first}, interactionRetrieveHeader: {"true"}}
	response := service.interceptContinuation(jsonFixture(t, map[string]any{
		"SourceFormat": "interactions", "Model": interactionOmniModel,
		"Body": []byte(body), "Headers": headers,
		"Metadata": map[string]string{"caller_scope": testCallerScope},
	}))
	if response.Terminate {
		t.Fatalf("interceptor rejected reference create: %+v", response)
	}
	for _, name := range response.ClearHeaders {
		headers.Del(name)
	}
	for name, values := range response.Headers {
		headers[name] = values
	}
	pick, err := service.pickContinuation(jsonFixture(t, map[string]any{
		"Providers": []string{provider}, "Model": interactionOmniModel,
		"Options":    map[string]any{"Headers": headers, "Metadata": map[string]string{"caller_scope": testCallerScope}},
		"Candidates": []any{map[string]string{"ID": target.Target.ID, "Provider": provider}},
	}))
	if err != nil || !pick.Handled || pick.AuthID != target.Target.ID {
		t.Fatalf("source excluded from generation candidates: pick=%+v err=%v", pick, err)
	}
	next := interactionID(t, interactionCall(t, service, target, body))
	stored, err := service.sessions.read(target.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	turns, err := continuationTurns(stored)
	if err != nil {
		t.Fatal(err)
	}
	turn := turns[continuationKey(next)]
	if next == first || turn.Parent != "" || turn.State != "complete" || !turn.ResultStored || turn.CallerScope != testCallerScope {
		t.Fatalf("reference did not create an independent stored turn: %+v", turn)
	}
	foreign := interactionExecutorRequest(t, target, body)
	foreign.Metadata.CallerScope = strings.Repeat("d", 64)
	if _, err := service.executeInteraction(t.Context(), foreign); safeCredentialCode(err) != "continuation_identity_mismatch" {
		t.Fatalf("cross-caller reference was not rejected: %v", err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 2 || len(fixture.uploads) != 1 {
		t.Fatalf("submissions=%d uploads=%d", len(fixture.fields), len(fixture.uploads))
	}
	if !bytes.Equal(fixture.uploads[0], []byte("0000ftypvideo")) || fixture.uploadCookies[0] != "target-cookie" {
		t.Fatal("the source video was not uploaded with the target account")
	}
	if jsonField(fixture.fields[1], 2, 0) != "" || jsonField(fixture.fields[1], 0, 3, 0, 0, 0) != "/uploaded/video" {
		t.Fatal("follow-up used conversation state instead of an uploaded video")
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

// A carried chain names the video instead of carrying its bytes, so the request
// stays the size it was; and the omni validator has to accept that name, or the
// turn is rejected before it is ever submitted.
func TestACarriedChainNamesTheVideoAndSurvivesValidation(t *testing.T) {
	location := chainedLocation{Account: "ref-1", Key: continuationKey(strings.Repeat("a", 64)), Caller: strings.Repeat("d", 64)}
	payload := []byte(`{"contents":[{"role":"user","parts":[{"text":"now further away"}]}]}`)

	carried, err := withChainedReference(payload, location)
	if err != nil {
		t.Fatal(err)
	}

	if err := validateOmni(carried); err != nil {
		t.Fatalf("the omni route rejected its own reference: %v", err)
	}
	if _, media, err := webContentsToPrompt(carried); err != nil {
		t.Fatalf("the prompt builder rejected its own reference: %v", err)
	} else if len(media) != 1 || media[0].Reference != location.String() {
		t.Fatalf("media = %+v, want the chained video named once", media)
	}
	var body struct {
		Contents []struct {
			Parts []map[string]json.RawMessage `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(carried, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Contents) != 1 || len(body.Contents[0].Parts) != 2 {
		t.Fatalf("parts = %+v, want the prompt and the reference", body.Contents)
	}
	if len(carried) > len(payload)+256 {
		t.Fatalf("carried body grew by %d bytes, want a name rather than the video", len(carried)-len(payload))
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
	host := &loginHostFixture{
		records: map[string]json.RawMessage{
			local.Target.ID: jsonFixture(t, local.Target),
			other.Target.ID: jsonFixture(t, other.Target),
		},
		service: service,
	}
	service.host = host.call
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
