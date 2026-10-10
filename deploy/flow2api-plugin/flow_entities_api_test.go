package main

import (
	"context"
	"errors"
	"net/url"
	"testing"
)

const (
	flowEntHero      = "eeeeeeee-1111-4222-8333-444444444444"
	flowEntRetired   = "eeeeeeee-2222-4222-8333-444444444444"
	flowEntProp      = "eeeeeeee-3333-4222-8333-444444444444"
	flowEntCopy      = "eeeeeeee-4444-4222-8333-444444444444"
	flowEntImage     = "66666666-7777-4888-9999-bbbbbbbbbbbb"
	flowEntImageFlow = "bbbbbbbb-4444-4ddd-8eee-ffffffffffff"
)

func flowEntHeroRow(images ...any) []any {
	return []any{
		flowResProject, flowEntHero, nil,
		[]any{1, "Hero", []any{images, []any{[]any{"achernar"}}, "Brave"}, true, false},
		flowTestMedia, []any{1024, 1024}, []any{"1700000000", 5}, []any{"1700000100"},
	}
}

func flowEntHeroImages() []any {
	return []any{[]any{flowResWorkflow}, []any{flowResOldFlow}}
}

func flowEntContents(fixture *flowFixture, hero []any) []any {
	contents := flowResContentsOf(fixture, flowResProject)
	media, _ := contents[2].([]any)
	contents[2] = append(media, []any{
		flowEntImage, flowResProject, flowEntImageFlow, nil, nil, nil,
		[]any{nil, []any{nil, nil, nil, nil, nil, nil, "image/png"}},
	})
	contents[5] = []any{
		hero,
		[]any{flowResProject, flowEntRetired, flowResTrash, []any{1, "Retired", []any{}, false, true}},
		[]any{flowResProject, flowEntProp, nil, []any{2, "Lantern"}},
	}
	return contents
}

func newFlowEntHarness(t *testing.T) *flowResHarness {
	t.Helper()
	harness := newFlowResHarness(t)
	harness.setContents(flowEntContents(harness.fixture, flowEntHeroRow(flowEntHeroImages()...)))
	return harness
}

func flowEntCall(harness *flowResHarness, method, tail, body string, query url.Values) (httpResponse, *publicError) {
	harness.t.Helper()
	response, err := harness.service.flowEntityHTTP(context.Background(), harness.record, flowResProject, tail,
		flowHTTPRequest{Method: method, Body: []byte(body), Query: query})
	if err == nil {
		return response, nil
	}
	var public *publicError
	if !errors.As(err, &public) {
		harness.t.Fatalf("unexpected non-public error: %v", err)
	}
	return response, public
}

func flowEntOK(harness *flowResHarness, method, tail, body string, status int) httpResponse {
	harness.t.Helper()
	response, failure := flowEntCall(harness, method, tail, body, nil)
	if failure != nil || response.StatusCode != status {
		harness.t.Fatalf("%s %s: status=%d failure=%+v body=%s", method, tail, response.StatusCode, failure, response.Body)
	}
	return response
}

func flowEntRejected(harness *flowResHarness, method, tail, body string, status int, code string) {
	harness.t.Helper()
	_, failure := flowEntCall(harness, method, tail, body, nil)
	if failure == nil || failure.HTTPStatus != status || failure.Code != code {
		harness.t.Fatalf("%s %s: failure=%+v, want %d %s", method, tail, failure, status, code)
	}
}

func flowEntIDs(list flowEntityList) []string {
	ids := []string{}
	for _, entity := range list.Entities {
		ids = append(ids, entity.ID)
	}
	return ids
}

func TestFlowEntityListDecodesCharactersAndFilters(t *testing.T) {
	harness := newFlowEntHarness(t)
	for _, scenario := range []struct {
		query url.Values
		want  string
	}{
		{nil, `["` + flowEntHero + `"]`},
		{url.Values{"archived": {"true"}}, `["` + flowEntRetired + `"]`},
		{url.Values{"archived": {"true"}, "collectionId": {flowResTrash}}, `["` + flowEntRetired + `"]`},
		{url.Values{"collectionId": {""}}, `["` + flowEntHero + `"]`},
		{url.Values{"collectionId": {flowResFolder}}, `[]`},
		{url.Values{"search": {"HER"}}, `["` + flowEntHero + `"]`},
		{url.Values{"search": {"lantern"}}, `[]`},
	} {
		response, failure := flowEntCall(harness, "GET", "", "", scenario.query)
		if failure != nil || response.StatusCode != 200 {
			t.Fatalf("query %v: failure=%+v", scenario.query, failure)
		}
		if got := flowResJSON(t, flowEntIDs(flowResDecode[flowEntityList](t, response))); got != scenario.want {
			t.Fatalf("query %v: ids=%s want %s", scenario.query, got, scenario.want)
		}
	}
	if _, failure := flowEntCall(harness, "GET", "", "", url.Values{"archived": {"maybe"}}); failure == nil || failure.Code != "flow_archived_filter_invalid" {
		t.Fatalf("failure=%+v", failure)
	}
}

func TestFlowEntityGetDecodesNamedFields(t *testing.T) {
	harness := newFlowEntHarness(t)

	hero := flowResDecode[flowEntityResource](t, flowEntOK(harness, "GET", flowEntHero, "", 200))

	want := `{"id":"` + flowEntHero + `","projectId":"` + flowResProject + `","name":"Hero","kind":"character","personalityNotes":"Brave",` +
		`"images":[{"slot":0,"workflowId":"` + flowResWorkflow + `"},{"slot":1,"workflowId":"` + flowResOldFlow + `"}],"voiceIds":["achernar"],` +
		`"primaryMediaId":"` + flowTestMedia + `","width":1024,"height":1024,"archived":false,"favorited":true,` +
		`"createdAt":"2023-11-14T22:13:20.000000005Z","updatedAt":"2023-11-14T22:15:00Z"}`
	if got := flowResJSON(t, hero); got != want {
		t.Fatalf("hero=%s\nwant %s", got, want)
	}
	flowEntRejected(harness, "GET", flowEntProp, "", 404, "flow_entity_not_found")
	flowEntRejected(harness, "GET", flowResOtherProj, "", 404, "flow_entity_not_found")
	harness.wantNoCall("JCZWLc")
}

func TestFlowEntityCreateUsesTheCapturedWire(t *testing.T) {
	harness := newFlowEntHarness(t)
	harness.reply("C4BZMd", []any{[]any{flowResProject, flowEntCopy, nil, []any{1, "Nova", []any{}}}})

	created := flowResDecode[flowEntityResource](t, flowEntOK(harness, "POST", "", `{"name":" Nova "}`, 201))

	harness.wantArgs("C4BZMd", []any{[]any{flowResProject, nil, nil, []any{1, "Nova", []any{}}}})
	if created.ID != flowEntCopy || created.Name != "Nova" || created.Images == nil || len(created.Images) != 0 || created.VoiceIDs == nil {
		t.Fatalf("created=%+v", created)
	}
	harness.wantNoCall("Zzl0ze")
}

func TestFlowEntityCreateIntoACollectionChecksItFirst(t *testing.T) {
	harness := newFlowEntHarness(t)
	harness.reply("C4BZMd", []any{[]any{flowResProject, flowEntCopy, flowResFolder, []any{1, "Nova", []any{}}}})

	created := flowResDecode[flowEntityResource](t, flowEntOK(harness, "POST", "", `{"name":"Nova","collectionId":"`+flowResFolder+`"}`, 201))

	harness.wantArgs("C4BZMd", []any{[]any{flowResProject, nil, flowResFolder, []any{1, "Nova", []any{}}}})
	if created.CollectionID != flowResFolder {
		t.Fatalf("created=%+v", created)
	}
}

func TestFlowEntityCreateRejectsBadInputBeforeUpstream(t *testing.T) {
	harness := newFlowEntHarness(t)
	flowEntRejected(harness, "POST", "", `{}`, 400, "flow_title_invalid")
	flowEntRejected(harness, "POST", "", `{"name":"bad\u0000name"}`, 400, "flow_title_invalid")
	flowEntRejected(harness, "POST", "", `{"name":"Nova","collectionId":"`+flowResOtherProj+`"}`, 404, "flow_collection_not_found")
	flowEntRejected(harness, "POST", "", `{"name":"Nova","kind":"prop"}`, 400, "flow_unsupported_generation_option")
	flowEntRejected(harness, "POST", "", ``, 400, "flow_unsupported_generation_option")
	harness.wantNoCall("C4BZMd")
}

func TestFlowEntityUpdateSendsOnlyMaskedFields(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		body  string
		entry []any
		mask  []any
	}{
		{
			"voices", `{"voiceIds":["achernar"]}`,
			[]any{flowResProject, flowEntHero, nil, []any{1, nil, []any{nil, []any{[]any{"achernar"}}}}},
			[]any{"entity_info.character_info.audio_references"},
		},
		{
			"voices cleared", `{"voiceIds":[]}`,
			[]any{flowResProject, flowEntHero, nil, []any{1, nil, []any{nil, []any{}}}},
			[]any{"entity_info.character_info.audio_references"},
		},
		{
			"preset voice reference", `{"voiceIds":["voices/achernar"]}`,
			[]any{flowResProject, flowEntHero, nil, []any{1, nil, []any{nil, []any{[]any{"voices/achernar"}}}}},
			[]any{"entity_info.character_info.audio_references"},
		},
		{
			"everything", `{"name":"Heroine","personalityNotes":"Calm\nsteady","voiceIds":["puck","achernar"],"favorited":false,"archived":false,"collectionId":"` + flowResFolder + `"}`,
			[]any{flowResProject, flowEntHero, flowResFolder, []any{1, "Heroine", []any{nil, []any{[]any{"puck"}, []any{"achernar"}}, "Calm\nsteady"}, false, false}},
			[]any{
				"entity_info.display_name", "entity_info.character_info.personality_notes", "entity_info.character_info.audio_references",
				"entity_info.is_favorited", "entity_info.archived", "collection_id",
			},
		},
		{
			"notes cleared", `{"personalityNotes":""}`,
			[]any{flowResProject, flowEntHero, nil, []any{1, nil, []any{nil, nil, ""}}},
			[]any{"entity_info.character_info.personality_notes"},
		},
		{
			"back to the root", `{"collectionId":""}`,
			[]any{flowResProject, flowEntHero, "", []any{1}},
			[]any{"collection_id"},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			harness := newFlowEntHarness(t)
			harness.reply("rzMKMb", []any{flowEntHeroRow(flowEntHeroImages()...)})

			updated := flowResDecode[flowEntityResource](t, flowEntOK(harness, "PATCH", flowEntHero, scenario.body, 200))

			harness.wantArgs("rzMKMb", []any{scenario.entry, []any{scenario.mask}})
			if updated.ID != flowEntHero || len(updated.Images) != 2 || harness.fixture.count("rzMKMb") != 1 {
				t.Fatalf("updated=%+v calls=%d", updated, harness.fixture.count("rzMKMb"))
			}
		})
	}
}

func TestFlowEntityUpdateRefusesInvalidChangesBeforeUpstream(t *testing.T) {
	harness := newFlowEntHarness(t)
	flowEntRejected(harness, "PATCH", flowEntHero, `{}`, 400, "flow_entity_request_invalid")
	flowEntRejected(harness, "PATCH", flowEntHero, `{"name":""}`, 400, "flow_title_invalid")
	flowEntRejected(harness, "PATCH", flowEntHero, `{"personalityNotes":"a\u0000b"}`, 400, "flow_entity_notes_invalid")
	flowEntRejected(harness, "PATCH", flowEntHero, `{"voiceIds":["achernar","achernar"]}`, 400, "flow_resource_ids_invalid")
	flowEntRejected(harness, "PATCH", flowEntHero, `{"voiceIds":["has space"]}`, 400, "flow_resource_ids_invalid")
	flowEntRejected(harness, "PATCH", flowEntHero, `{"collectionId":"`+flowResOtherProj+`"}`, 404, "flow_collection_not_found")
	flowEntRejected(harness, "PATCH", flowEntHero, `{"kind":"prop"}`, 400, "flow_unsupported_generation_option")
	flowEntRejected(harness, "PATCH", flowEntProp, `{"name":"x"}`, 404, "flow_entity_not_found")
	harness.wantNoCall("rzMKMb")
}

func TestFlowEntityUpdateRejectsAnotherEntityInTheReply(t *testing.T) {
	harness := newFlowEntHarness(t)
	harness.reply("rzMKMb", []any{[]any{flowResProject, flowEntCopy, nil, []any{1, "Other"}}})

	flowEntRejected(harness, "PATCH", flowEntHero, `{"name":"Heroine"}`, 502, "flow_entity_identity_mismatch")
}

func TestFlowEntityTrashRestoreAndPurge(t *testing.T) {
	harness := newFlowEntHarness(t)
	harness.reply("rzMKMb", []any{[]any{flowResProject, flowEntHero, nil, []any{1, "Hero", []any{}, true, true}}})
	flowEntOK(harness, "DELETE", flowEntHero, "", 204)
	harness.wantArgs("rzMKMb", []any{[]any{flowResProject, flowEntHero, nil, []any{1, nil, nil, nil, true}}, []any{[]any{"entity_info.archived"}}})

	flowEntRejected(harness, "POST", flowEntHero+":purge", "", 409, "flow_entity_not_archived")
	harness.wantNoCall("cz8Z4b")

	restoreHarness := newFlowEntHarness(t)
	restoreHarness.reply("rzMKMb", []any{[]any{flowResProject, flowEntRetired, nil, []any{1, "Retired", []any{}, false, false}}})
	restored := flowResDecode[flowEntityResource](t, flowEntOK(restoreHarness, "POST", flowEntRetired+":restore", "", 200))
	restoreHarness.wantArgs("rzMKMb", []any{[]any{flowResProject, flowEntRetired, nil, []any{1, nil, nil, nil, false}}, []any{[]any{"entity_info.archived"}}})
	if restored.Archived || restored.ID != flowEntRetired {
		t.Fatalf("restored=%+v", restored)
	}

	purgeHarness := newFlowEntHarness(t)
	purgeHarness.reply("cz8Z4b", []any{})
	flowEntOK(purgeHarness, "POST", flowEntRetired+":purge", "", 204)
	purgeHarness.wantArgs("cz8Z4b", []any{nil, nil, flowResProject, []any{flowEntRetired}})
}

func TestFlowEntityCopyStaysInTheProject(t *testing.T) {
	harness := newFlowEntHarness(t)
	harness.reply("YIBQMe", []any{[]any{flowResProject, flowEntCopy, nil, []any{1, "Hero", []any{}}}})

	copied := flowResDecode[flowEntityResource](t, flowEntOK(harness, "POST", flowEntHero+":copy", ``, 201))

	harness.wantArgs("YIBQMe", []any{flowResProject, flowEntHero})
	if copied.ID != flowEntCopy || copied.Name != "Hero" {
		t.Fatalf("copied=%+v", copied)
	}
	flowEntRejected(harness, "POST", flowEntHero+":copy", `{"sourceProjectId":"`+flowResOtherProj+`"}`, 400, "flow_unsupported_generation_option")
	flowEntRejected(harness, "POST", flowEntProp+":copy", `{}`, 404, "flow_entity_not_found")
	if harness.fixture.count("YIBQMe") != 1 {
		t.Fatalf("copy calls=%d", harness.fixture.count("YIBQMe"))
	}
}

func TestFlowEntityCopyRejectsTheSourceInTheReply(t *testing.T) {
	harness := newFlowEntHarness(t)
	harness.reply("YIBQMe", []any{flowEntHeroRow()})

	flowEntRejected(harness, "POST", flowEntHero+":copy", `{}`, 502, "flow_entity_identity_mismatch")
}

func TestFlowEntityImageCopySendsTheCharacterSlotAndReadsItBack(t *testing.T) {
	harness := newFlowEntHarness(t)
	before := flowEntContents(harness.fixture, flowEntHeroRow(flowEntHeroImages()...))
	after := flowEntContents(harness.fixture, flowEntHeroRow(append(flowEntHeroImages(), []any{}, []any{}, []any{flowEntImageFlow})...))
	harness.setContentsQueue(before, after)
	harness.reply("Sc7aEb", []any{[]any{flowEntImageFlow}})

	updated := flowResDecode[flowEntityResource](t, flowEntOK(harness, "POST", flowEntHero+"/images", `{"mediaId":"`+flowEntImage+`","slot":4}`, 201))

	args := harness.fixture.args("Sc7aEb", 0)
	workflowSeed, mediaSeed := jsonField(args, 9), jsonField(args, 10)
	if len(args) != 11 || args[0] != flowEntImage || args[3] != flowResProject ||
		flowResJSON(t, args[7]) != flowResJSON(t, []any{nil, nil, []any{flowEntHero, []any{4}}}) ||
		flowResJSON(t, args[1:3]) != `[null,null]` || flowResJSON(t, args[4:7]) != `[null,null,null]` || args[8] != nil {
		t.Fatalf("args=%s", flowResJSON(t, args))
	}
	for _, seed := range []any{workflowSeed, mediaSeed} {
		if identifier, ok := seed.(string); !ok || !flowUUIDPattern.MatchString(identifier) {
			t.Fatalf("seed=%v in %s", seed, flowResJSON(t, args))
		}
	}
	if harness.fixture.count("Zzl0ze") != 2 || len(updated.Images) != 3 || updated.Images[2].Slot != 4 || updated.Images[2].WorkflowID != flowEntImageFlow {
		t.Fatalf("reads=%d updated=%+v", harness.fixture.count("Zzl0ze"), updated)
	}
}

func TestFlowEntityImageCopyValidatesMediaAndSlotBeforeUpstream(t *testing.T) {
	harness := newFlowEntHarness(t)
	flowEntRejected(harness, "POST", flowEntHero+"/images", `{"mediaId":"`+flowEntImage+`","slot":2147483648}`, 400, "flow_entity_slot_invalid")
	flowEntRejected(harness, "POST", flowEntHero+"/images", `{"mediaId":"`+flowEntImage+`","slot":-1}`, 400, "flow_entity_slot_invalid")
	flowEntRejected(harness, "POST", flowEntHero+"/images", `{"mediaId":"`+flowEntImage+`"}`, 400, "flow_entity_request_invalid")
	flowEntRejected(harness, "POST", flowEntHero+"/images", `{"slot":0}`, 400, "flow_entity_request_invalid")
	flowEntRejected(harness, "POST", flowEntHero+"/images", `{"mediaId":"`+flowTestMedia+`","slot":0}`, 400, "flow_entity_reference_requires_image")
	flowEntRejected(harness, "POST", flowEntHero+"/images", `{"mediaId":"`+flowResOtherProj+`","slot":0}`, 404, "flow_media_not_found")
	flowEntRejected(harness, "POST", flowEntProp+"/images", `{"mediaId":"`+flowEntImage+`","slot":0}`, 404, "flow_entity_not_found")
	flowEntRejected(harness, "POST", flowEntHero+"/images", `{"mediaId":"`+flowEntImage+`","slot":0,"url":"https://example.test/a.png"}`, 400, "flow_unsupported_generation_option")
	harness.wantNoCall("Sc7aEb")
}

func TestFlowEntityImageCopyIsNotReplayedWhenTheReadBackFails(t *testing.T) {
	harness := newFlowEntHarness(t)
	before := rpcEnvelope(t, "Zzl0ze", flowEntContents(harness.fixture, flowEntHeroRow(flowEntHeroImages()...)))
	harness.fixture.mu.Lock()
	harness.fixture.replies["Zzl0ze"] = []string{before, flowErrorEnvelope(t, []any{"er", nil, nil, nil, nil, 500, "generic"})}
	harness.fixture.mu.Unlock()
	harness.reply("Sc7aEb", []any{[]any{flowEntImageFlow}})

	flowEntRejected(harness, "POST", flowEntHero+"/images", `{"mediaId":"`+flowEntImage+`","slot":0}`, 502, "flow_entity_refresh_failed")

	if harness.fixture.count("Sc7aEb") != 1 {
		t.Fatalf("copy calls=%d, want exactly one", harness.fixture.count("Sc7aEb"))
	}
}

func TestFlowEntityImageDeleteClearsTheSlot(t *testing.T) {
	harness := newFlowEntHarness(t)
	harness.reply("LXpojc", []any{flowEntHeroRow([]any{flowResWorkflow})})

	updated := flowResDecode[flowEntityResource](t, flowEntOK(harness, "DELETE", flowEntHero+"/images/1", "", 200))

	harness.wantArgs("LXpojc", []any{nil, flowResProject, flowEntHero, 1})
	if len(updated.Images) != 1 || updated.Images[0].Slot != 0 {
		t.Fatalf("updated=%+v", updated)
	}
}

func TestFlowEntityImageDeleteRejectsUnknownSlotsBeforeUpstream(t *testing.T) {
	harness := newFlowEntHarness(t)
	flowEntRejected(harness, "DELETE", flowEntHero+"/images/2", "", 404, "flow_entity_image_not_found")
	flowEntRejected(harness, "DELETE", flowEntHero+"/images/x", "", 404, "flow_route_not_found")
	flowEntRejected(harness, "DELETE", flowEntHero+"/images/-1", "", 404, "flow_route_not_found")
	flowEntRejected(harness, "DELETE", flowEntProp+"/images/0", "", 404, "flow_entity_not_found")
	flowEntRejected(harness, "GET", flowEntHero+"/images", "", 404, "flow_route_not_found")
	flowEntRejected(harness, "POST", flowEntHero+"/images/0", "", 404, "flow_route_not_found")
	harness.wantNoCall("LXpojc")
}

func TestFlowEntityMutationIsNotReplayedAfterAnUpstreamFailure(t *testing.T) {
	harness := newFlowEntHarness(t)
	harness.fixture.reply("rzMKMb", flowErrorEnvelope(t, []any{"er", nil, nil, nil, nil, 500, "generic"}))

	if _, failure := flowEntCall(harness, "PATCH", flowEntHero, `{"name":"Heroine"}`, nil); failure == nil {
		t.Fatal("upstream failure was swallowed")
	}

	if harness.fixture.count("rzMKMb") != 1 {
		t.Fatalf("update calls=%d, want exactly one", harness.fixture.count("rzMKMb"))
	}
}

func TestFlowEntityRoutesRejectUnknownShapes(t *testing.T) {
	harness := newFlowEntHarness(t)
	flowEntRejected(harness, "PUT", "", "", 404, "flow_route_not_found")
	flowEntRejected(harness, "POST", flowEntHero+":explode", "", 404, "flow_route_not_found")
	flowEntRejected(harness, "PUT", flowEntHero, "", 404, "flow_route_not_found")
	flowEntRejected(harness, "GET", flowEntHero+"/media", "", 404, "flow_route_not_found")
	flowEntRejected(harness, "GET", "has space", "", 404, "flow_route_not_found")
}
