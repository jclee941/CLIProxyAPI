package main

import (
	"net/url"
	"testing"
)

func flowResClipRow(workflow, primary string, position int, duration, start, end any) []any {
	metadata := []any{}
	if primary != "" {
		metadata = []any{nil, nil, nil, nil, primary}
	}
	return []any{[]any{workflow, nil, nil, metadata}, flowResScene, []any{position, duration, start, end}}
}

func flowResSceneIDs(list flowSceneList) []string {
	ids := []string{}
	for _, scene := range list.Scenes {
		ids = append(ids, scene.ID)
	}
	return ids
}

func TestFlowSceneListDecodesAndFiltersScenes(t *testing.T) {
	harness := newFlowResHarness(t)
	for _, scenario := range []struct {
		query url.Values
		want  string
	}{
		{nil, `["` + flowResScene + `"]`},
		{url.Values{"archived": {"true"}}, `["` + flowResFiled + `"]`},
		{url.Values{"archived": {"true"}, "collectionId": {flowResTrash}}, `["` + flowResFiled + `"]`},
		{url.Values{"collectionId": {flowResFolder}}, `[]`},
		{url.Values{"search": {"open"}}, `["` + flowResScene + `"]`},
	} {
		response, failure := harness.call("scenes", "GET", "", "", scenario.query)
		if failure != nil || response.StatusCode != 200 {
			t.Fatalf("query %v: failure=%+v", scenario.query, failure)
		}
		list := flowResDecode[flowSceneList](t, response)
		if got := flowResJSON(t, flowResSceneIDs(list)); got != scenario.want {
			t.Fatalf("query %v: ids=%s want %s", scenario.query, got, scenario.want)
		}
		for _, scene := range list.Scenes {
			if scene.ID == flowResScene && (scene.AspectRatio != "16:9" || scene.ProjectID != flowResProject) ||
				scene.ID == flowResFiled && (scene.AspectRatio != "9:16" || scene.CollectionID != flowResTrash || !scene.Archived) {
				t.Fatalf("scene=%+v", scene)
			}
		}
	}
}

func TestFlowSceneCreateUsesTheCapturedWireAndLiveResponse(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("rqZuUc", []any{[]any{flowResSceneCopy, "Untitled", nil, nil, nil, 2, nil, []any{}}})

	response := harness.ok("scenes", "POST", "", `{"aspectRatio":"16:9"}`, 201)

	harness.wantArgs("rqZuUc", []any{"projects/" + flowResProject, nil, nil, nil, 2})
	created := flowResDecode[flowSceneDetail](t, response)
	if created.ID != flowResSceneCopy || created.AspectRatio != "16:9" || created.Clips == nil || len(created.Clips) != 0 {
		t.Fatalf("created=%+v", created)
	}
	harness.wantNoCall("uwAyfb")
}

func TestFlowSceneCreateFromWorkflowsReadsBackTheClips(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("rqZuUc", []any{[]any{flowResSceneCopy, "Untitled", nil, nil, nil, 1, flowResFolder, []any{}}})
	harness.reply("uwAyfb", []any{[]any{flowResClipRow(flowResWorkflow, flowTestMedia, 0, []any{8}, nil, nil)}, []any{}})

	response := harness.ok("scenes", "POST", "", `{"aspectRatio":"9:16","collectionId":"`+flowResFolder+`","workflowIds":["`+flowResWorkflow+`"]}`, 201)

	harness.wantArgs("rqZuUc", []any{"projects/" + flowResProject, []any{flowResWorkflow}, flowResFolder, nil, 1})
	harness.wantArgs("uwAyfb", []any{flowResSceneCopy, flowResProject})
	created := flowResDecode[flowSceneDetail](t, response)
	if len(created.Clips) != 1 || created.Clips[0].WorkflowID != flowResWorkflow || created.Clips[0].PrimaryMediaID != flowTestMedia ||
		created.Clips[0].DurationSeconds != 8 || created.Clips[0].EndSeconds != 8 || created.CollectionID != flowResFolder {
		t.Fatalf("created=%+v", created)
	}
}

func TestFlowSceneCreateRejectsBadInputBeforeUpstream(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.rejected("scenes", "POST", "", `{"aspectRatio":"4:3"}`, 400, "flow_scene_aspect_invalid")
	harness.rejected("scenes", "POST", "", `{}`, 400, "flow_scene_aspect_invalid")
	harness.rejected("scenes", "POST", "", `{"aspectRatio":"16:9","workflowIds":["`+flowResOtherFlow+`"]}`, 404, "flow_workflow_not_found")
	harness.rejected("scenes", "POST", "", `{"aspectRatio":"16:9","collectionId":"`+flowResOtherProj+`"}`, 404, "flow_collection_not_found")
	harness.rejected("scenes", "POST", "", `{"aspectRatio":"16:9","title":"x"}`, 400, "flow_unsupported_generation_option")
	harness.wantNoCall("rqZuUc")
}

func TestFlowSceneGetReturnsClipsWithOffsets(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("uwAyfb", []any{[]any{
		flowResClipRow(flowResOtherFlow, "", 1, nil, nil, nil),
		flowResClipRow(flowResWorkflow, flowTestMedia, 0, []any{8}, []any{"1"}, []any{6, 500000000}),
	}, []any{}})

	response := harness.ok("scenes", "GET", flowResScene, "", 200)

	harness.wantArgs("uwAyfb", []any{flowResScene, flowResProject})
	scene := flowResDecode[flowSceneDetail](t, response)
	if len(scene.Clips) != 2 || scene.Clips[0].WorkflowID != flowResWorkflow || scene.Clips[0].StartSeconds != 1 || scene.Clips[0].EndSeconds != 6.5 ||
		scene.Clips[1].Position != 1 || scene.Clips[1].DurationSeconds != 8 || scene.Clips[1].EndSeconds != 8 {
		t.Fatalf("scene=%+v", scene)
	}
}

func TestFlowSceneUpdateSendsMaskedFields(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("BpMsoe", []any{[]any{flowResScene, "Renamed", nil, nil, nil, 2, flowResFolder, []any{true}}})

	response := harness.ok("scenes", "PATCH", flowResScene, `{"title":"Renamed","collectionId":"`+flowResFolder+`","archived":true}`, 200)

	harness.wantArgs("BpMsoe", []any{
		flowResProject, flowResScene,
		[]any{flowResScene, "Renamed", nil, nil, nil, nil, flowResFolder, []any{true}},
		[]any{[]any{"display_name", "parent_collection_id", "scene_metadata.is_archived"}},
	})
	if updated := flowResDecode[flowSceneResource](t, response); updated.Title != "Renamed" || updated.CollectionID != flowResFolder || !updated.Archived {
		t.Fatalf("updated=%+v", updated)
	}
}

func TestFlowSceneAspectAndFavoriteUseTheirOwnMaskPaths(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("BpMsoe", []any{[]any{flowResScene, "Opening", nil, nil, nil, 1, nil, []any{nil, true}}})

	response := harness.ok("scenes", "PATCH", flowResScene, `{"aspectRatio":"9:16","favorited":true}`, 200)

	harness.wantArgs("BpMsoe", []any{
		flowResProject, flowResScene,
		[]any{flowResScene, nil, nil, nil, nil, 1, nil, []any{nil, true}},
		[]any{[]any{"aspect_ratio", "scene_metadata.is_favorited"}},
	})
	if updated := flowResDecode[flowSceneResource](t, response); updated.AspectRatio != "9:16" || !updated.Favorited {
		t.Fatalf("updated=%+v", updated)
	}
}

func TestFlowSceneUpdateRefusesInvalidChangesBeforeUpstream(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.rejected("scenes", "PATCH", flowResScene, `{}`, 400, "flow_scene_request_invalid")
	harness.rejected("scenes", "PATCH", flowResScene, `{"aspectRatio":"1:1"}`, 400, "flow_scene_aspect_invalid")
	harness.rejected("scenes", "PATCH", flowResScene, `{"collectionId":"`+flowResOtherProj+`"}`, 404, "flow_collection_not_found")
	harness.rejected("scenes", "PATCH", flowResOtherProj, `{"title":"x"}`, 404, "flow_scene_not_found")
	harness.wantNoCall("BpMsoe")
}

func TestFlowSceneTrashRestoreAndPurge(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("BpMsoe", []any{[]any{flowResScene, "Opening", nil, nil, nil, 2, nil, []any{true}}})
	harness.ok("scenes", "DELETE", flowResScene, "", 204)
	harness.wantArgs("BpMsoe", []any{
		flowResProject, flowResScene, []any{flowResScene, nil, nil, nil, nil, nil, nil, []any{true}},
		[]any{[]any{"scene_metadata.is_archived"}},
	})

	harness.rejected("scenes", "POST", flowResScene+":purge", "", 409, "flow_scene_not_archived")
	harness.wantNoCall("cz8Z4b")
	harness.reply("cz8Z4b", []any{})
	harness.ok("scenes", "POST", flowResFiled+":purge", "", 204)
	harness.wantArgs("cz8Z4b", []any{nil, nil, flowResProject, nil, []any{flowResFiled}})
}

func TestFlowSceneCopyKeepsSourceAndDestinationProjects(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.setContentsQueue(flowResContents(harness.fixture), flowResContentsOf(harness.fixture, flowResOtherProj))
	harness.reply("OSd63c", []any{[]any{flowResSceneCopy, "Opening", nil, nil, nil, 2, flowResFolder, []any{}}, []any{flowResClipRow(flowResWorkflow, "", 0, []any{8}, nil, nil)}})
	harness.reply("uwAyfb", []any{[]any{flowResClipRow(flowResWorkflow, flowTestMedia, 0, []any{8}, nil, nil)}, []any{}})

	response := harness.ok("scenes", "POST", flowResScene+":copy", `{"sourceProjectId":"`+flowResOtherProj+`","collectionId":"`+flowResFolder+`"}`, 201)

	harness.wantArgs("OSd63c", []any{flowResOtherProj, flowResScene, flowResFolder, flowResProject})
	harness.wantArgs("uwAyfb", []any{flowResSceneCopy, flowResProject})
	if harness.fixture.count("Zzl0ze") != 2 {
		t.Fatalf("contents reads=%d, want destination and source", harness.fixture.count("Zzl0ze"))
	}
	if copied := flowResDecode[flowSceneDetail](t, response); copied.ID != flowResSceneCopy || len(copied.Clips) != 1 || copied.Clips[0].PrimaryMediaID != flowTestMedia {
		t.Fatalf("copied=%+v", copied)
	}
}

func TestFlowSceneCopyWithinTheProjectNeedsNoSecondRead(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("OSd63c", []any{[]any{flowResSceneCopy, "Opening", nil, nil, nil, 2, nil, []any{}}, []any{}})

	harness.ok("scenes", "POST", flowResScene+":copy", `{}`, 201)

	harness.wantArgs("OSd63c", []any{flowResProject, flowResScene, nil, flowResProject})
	harness.wantNoCall("uwAyfb")
	harness.rejected("scenes", "POST", flowResOtherProj+":copy", `{}`, 404, "flow_scene_not_found")
	harness.rejected("scenes", "POST", flowResScene+":copy", `{"sourceProjectId":"not-a-project"}`, 400, "flow_scene_request_invalid")
}

func TestFlowSceneAddClipsSendsPositionAndOffsets(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("oWTRd", []any{[]any{flowResClipRow(flowResWorkflow, flowTestMedia, 1, []any{8}, []any{0, 500000000}, []any{6})}, []any{}})

	response := harness.ok("scenes", "POST", flowResScene+"/clips", `{"workflowIds":["`+flowResWorkflow+`"],"position":1,"startSeconds":0.5,"endSeconds":6}`, 201)

	harness.wantArgs("oWTRd", []any{flowResProject, flowResScene, []any{flowResWorkflow}, 1, []any{0, 500000000}, []any{6}})
	if added := flowResDecode[flowSceneClipList](t, response); len(added.Clips) != 1 || added.Clips[0].StartSeconds != 0.5 || added.Clips[0].EndSeconds != 6 {
		t.Fatalf("added=%+v", added)
	}
}

func TestFlowSceneAddClipsOmitsUnsetPosition(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("oWTRd", []any{[]any{}, []any{}})

	harness.ok("scenes", "POST", flowResScene+"/clips", `{"workflowIds":["`+flowResWorkflow+`"]}`, 201)

	harness.wantArgs("oWTRd", []any{flowResProject, flowResScene, []any{flowResWorkflow}})
}

func TestFlowSceneAddClipsRejectsBadInputBeforeUpstream(t *testing.T) {
	harness := newFlowResHarness(t)
	base := `"workflowIds":["` + flowResWorkflow + `"]`
	harness.rejected("scenes", "POST", flowResScene+"/clips", `{}`, 400, "flow_resource_ids_invalid")
	harness.rejected("scenes", "POST", flowResScene+"/clips", `{"workflowIds":["`+flowResOtherFlow+`"]}`, 404, "flow_workflow_not_found")
	harness.rejected("scenes", "POST", flowResScene+"/clips", `{`+base+`,"position":-1}`, 400, "flow_clip_range_invalid")
	harness.rejected("scenes", "POST", flowResScene+"/clips", `{`+base+`,"startSeconds":5,"endSeconds":5}`, 400, "flow_clip_range_invalid")
	harness.rejected("scenes", "POST", flowResOtherProj+"/clips", `{`+base+`}`, 404, "flow_scene_not_found")
	harness.wantNoCall("oWTRd")
}

func TestFlowSceneReorderRewritesTheWholeClipList(t *testing.T) {
	harness := newFlowResHarness(t)
	first := flowResClipRow(flowResWorkflow, flowTestMedia, 0, []any{8}, nil, nil)
	second := flowResClipRow(flowResOtherFlow, "", 1, []any{8}, nil, nil)
	harness.reply("uwAyfb", []any{[]any{first, second}, []any{}})
	harness.reply("GoMJte", []any{[]any{flowResClipRow(flowResOtherFlow, "", 0, []any{8}, []any{0}, []any{8}), flowResClipRow(flowResWorkflow, flowTestMedia, 1, []any{8}, []any{0}, []any{8})}})

	response := harness.ok("scenes", "POST", flowResScene+"/clips:reorder", `{"from":0,"to":1}`, 200)

	harness.wantArgs("GoMJte", []any{flowResProject, flowResScene, []any{
		[]any{[]any{flowResOtherFlow, nil, nil, []any{}}, flowResScene, []any{0, []any{8}, []any{0}, []any{8}}},
		[]any{[]any{flowResWorkflow, nil, nil, []any{nil, nil, nil, nil, flowTestMedia}}, flowResScene, []any{1, []any{8}, []any{0}, []any{8}}},
	}})
	if reordered := flowResDecode[flowSceneClipList](t, response); len(reordered.Clips) != 2 || reordered.Clips[0].WorkflowID != flowResOtherFlow {
		t.Fatalf("reordered=%+v", reordered)
	}
}

func TestFlowSceneReorderRejectsPositionsOutsideTheList(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("uwAyfb", []any{[]any{flowResClipRow(flowResWorkflow, "", 0, []any{8}, nil, nil)}, []any{}})
	harness.rejected("scenes", "POST", flowResScene+"/clips:reorder", `{"from":0,"to":1}`, 400, "flow_clip_position_invalid")
	harness.rejected("scenes", "POST", flowResScene+"/clips:reorder", `{"from":0}`, 400, "flow_clip_position_invalid")
	harness.wantNoCall("GoMJte")
}

func TestFlowSceneClipOffsetsStayInsideTheClipDuration(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("uwAyfb", []any{[]any{flowResClipRow(flowResWorkflow, "", 0, []any{8}, nil, nil)}, []any{}})
	harness.rejected("scenes", "PATCH", flowResScene+"/clips/0", `{"startSeconds":1,"endSeconds":9}`, 400, "flow_clip_range_invalid")
	harness.rejected("scenes", "PATCH", flowResScene+"/clips/0", `{"startSeconds":4,"endSeconds":4}`, 400, "flow_clip_range_invalid")
	harness.rejected("scenes", "PATCH", flowResScene+"/clips/0", `{}`, 400, "flow_clip_range_invalid")
	harness.rejected("scenes", "PATCH", flowResScene+"/clips/1", `{"startSeconds":1}`, 404, "flow_clip_not_found")
	harness.wantNoCall("GoMJte")
	harness.reply("GoMJte", []any{[]any{flowResClipRow(flowResWorkflow, "", 0, []any{8}, []any{1}, []any{6, 250000000})}})

	harness.ok("scenes", "PATCH", flowResScene+"/clips/0", `{"startSeconds":1,"endSeconds":6.25}`, 200)

	harness.wantArgs("GoMJte", []any{flowResProject, flowResScene, []any{
		[]any{[]any{flowResWorkflow, nil, nil, []any{}}, flowResScene, []any{0, []any{8}, []any{1}, []any{6, 250000000}}},
	}})
}

func TestFlowSceneDeleteClipRenumbersTheRest(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("uwAyfb", []any{[]any{
		flowResClipRow(flowResWorkflow, "", 0, []any{8}, nil, nil), flowResClipRow(flowResOtherFlow, "", 1, []any{8}, nil, nil),
	}, []any{}})
	harness.reply("GoMJte", []any{[]any{flowResClipRow(flowResOtherFlow, "", 0, []any{8}, []any{0}, []any{8})}})

	harness.ok("scenes", "DELETE", flowResScene+"/clips/0", "", 200)

	harness.wantArgs("GoMJte", []any{flowResProject, flowResScene, []any{
		[]any{[]any{flowResOtherFlow, nil, nil, []any{}}, flowResScene, []any{0, []any{8}, []any{0}, []any{8}}},
	}})
	harness.rejected("scenes", "DELETE", flowResScene+"/clips/5", "", 404, "flow_clip_not_found")
	harness.rejected("scenes", "DELETE", flowResScene+"/clips/x", "", 404, "flow_route_not_found")
}

func TestFlowSceneRoutesRejectUnknownShapes(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.rejected("scenes", "PUT", "", "", 404, "flow_route_not_found")
	harness.rejected("scenes", "POST", flowResScene+":explode", "", 404, "flow_route_not_found")
	harness.rejected("scenes", "GET", flowResScene+"/media", "", 404, "flow_route_not_found")
	harness.rejected("scenes", "GET", flowResOtherProj, "", 404, "flow_scene_not_found")
}
