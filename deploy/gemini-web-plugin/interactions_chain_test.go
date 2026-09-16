package main

import (
	"encoding/json"
	"strings"
	"testing"
)

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
