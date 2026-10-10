package main

import (
	"net/url"
	"testing"
)

func flowResWorkflowIDs(list flowWorkflowList) []string {
	ids := []string{}
	for _, workflow := range list.Workflows {
		ids = append(ids, workflow.ID)
	}
	return ids
}

func TestFlowWorkflowListJoinsMediaAndFiltersTrash(t *testing.T) {
	harness := newFlowResHarness(t)
	for _, scenario := range []struct {
		query url.Values
		want  string
	}{
		{nil, `["` + flowResWorkflow + `"]`},
		{url.Values{"archived": {"true"}}, `["` + flowResOldFlow + `"]`},
		{url.Values{"collectionId": {flowResFolder}, "search": {"FOX"}}, `["` + flowResWorkflow + `"]`},
		{url.Values{"collectionId": {""}}, `[]`},
		{url.Values{"archived": {"true"}, "collectionId": {""}}, `["` + flowResOldFlow + `"]`},
	} {
		response, failure := harness.call("workflows", "GET", "", "", scenario.query)
		if failure != nil || response.StatusCode != 200 {
			t.Fatalf("query %v: failure=%+v", scenario.query, failure)
		}
		list := flowResDecode[flowWorkflowList](t, response)
		if got := flowResJSON(t, flowResWorkflowIDs(list)); got != scenario.want {
			t.Fatalf("query %v: ids=%s want %s", scenario.query, got, scenario.want)
		}
	}
	response := harness.ok("workflows", "GET", flowResWorkflow, "", 200)
	fox := flowResDecode[flowWorkflowResource](t, response)
	if fox.CollectionID != flowResFolder || fox.Title != "Orange fox" || fox.PrimaryMediaID != flowTestMedia ||
		flowResJSON(t, fox.MediaIDs) != `["`+flowTestMedia+`"]` || fox.CreatedAt != "2023-11-14T22:13:20.000000005Z" {
		t.Fatalf("workflow=%+v", fox)
	}
}

func TestFlowWorkflowRejectsRecordsOfAnotherProject(t *testing.T) {
	harness := newFlowResHarness(t)
	contents := flowResContents(harness.fixture)
	contents[1] = []any{[]any{flowResWorkflow, nil, nil, []any{"Orange fox"}, flowResOtherProj}}
	harness.setContents(contents)
	harness.rejected("workflows", "GET", flowResWorkflow, "", 502, "flow_workflow_identity_mismatch")
	harness.rejected("workflows", "DELETE", flowResWorkflow, "", 502, "flow_workflow_identity_mismatch")
	harness.wantNoCall("mYWVGd")
}

func TestFlowWorkflowUpdateSendsMaskedMetadata(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("mYWVGd", []any{flowResWorkflow, "", nil, []any{"Renamed", nil, false, false, flowTestMedia}, flowResProject})

	response := harness.ok("workflows", "PATCH", flowResWorkflow,
		`{"title":"Renamed","collectionId":"","primaryMediaId":"`+flowTestMedia+`"}`, 200)

	harness.wantArgs("mYWVGd", []any{
		[]any{flowResWorkflow, "", nil, []any{"Renamed", nil, nil, nil, flowTestMedia}, flowResProject},
		[]any{[]any{"metadata.display_name", "metadata.primary_media_id", "collection_id"}},
	})
	updated := flowResDecode[flowWorkflowResource](t, response)
	if updated.Title != "Renamed" || updated.CollectionID != "" || flowResJSON(t, updated.MediaIDs) != `["`+flowTestMedia+`"]` {
		t.Fatalf("updated=%+v", updated)
	}
}

func TestFlowWorkflowFavoriteAndFolderMoveUseTheirOwnFields(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("mYWVGd", []any{flowResWorkflow, flowResChild, nil, []any{"Orange fox", nil, false, true}, flowResProject})

	harness.ok("workflows", "PATCH", flowResWorkflow, `{"favorited":true,"collectionId":"`+flowResChild+`"}`, 200)

	harness.wantArgs("mYWVGd", []any{
		[]any{flowResWorkflow, flowResChild, nil, []any{nil, nil, nil, true}, flowResProject},
		[]any{[]any{"metadata.favorited", "collection_id"}},
	})
}

func TestFlowWorkflowUpdateRefusesInvalidChangesBeforeUpstream(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.rejected("workflows", "PATCH", flowResWorkflow, `{}`, 400, "flow_workflow_request_invalid")
	harness.rejected("workflows", "PATCH", flowResWorkflow, `{"title":" "}`, 400, "flow_title_invalid")
	harness.rejected("workflows", "PATCH", flowResWorkflow, `{"primaryMediaId":"somebody-elses-media"}`, 404, "flow_media_not_found")
	harness.rejected("workflows", "PATCH", flowResWorkflow, `{"primaryMediaId":""}`, 404, "flow_media_not_found")
	harness.rejected("workflows", "PATCH", flowResWorkflow, `{"collectionId":"`+flowResOtherProj+`"}`, 404, "flow_collection_not_found")
	harness.rejected("workflows", "PATCH", flowResWorkflow, `{"project":"x"}`, 400, "flow_unsupported_generation_option")
	harness.rejected("workflows", "PATCH", flowResOtherFlow, `{"title":"x"}`, 404, "flow_workflow_not_found")
	harness.wantNoCall("mYWVGd")
}

func TestFlowWorkflowTrashAndRestoreOnlyChangeTheArchiveFlag(t *testing.T) {
	for _, archived := range []bool{true, false} {
		harness := newFlowResHarness(t)
		id := flowResWorkflow
		if !archived {
			id = flowResOldFlow
		}
		harness.reply("mYWVGd", []any{id, nil, nil, []any{"Orange fox", nil, archived}, flowResProject})
		if archived {
			harness.ok("workflows", "DELETE", id, "", 204)
		} else {
			harness.ok("workflows", "POST", id+":restore", "", 200)
		}
		message := []any{id, nil, nil, []any{nil, nil, archived}, flowResProject}
		harness.wantArgs("mYWVGd", []any{message, []any{[]any{"metadata.archived"}}})
	}
}

func TestFlowWorkflowPurgeDeletesOnlyTrashedWorkflowsWithTheirPrimaryMedia(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.rejected("workflows", "POST", flowResWorkflow+":purge", "", 409, "flow_workflow_not_archived")
	harness.wantNoCall("cz8Z4b")
	harness.reply("cz8Z4b", []any{})

	harness.ok("workflows", "POST", flowResOldFlow+":purge", "", 204)

	harness.wantArgs("cz8Z4b", []any{nil, []any{flowResOldFlow}, flowResProject, nil, nil, nil, []any{flowResPrimaryOld}})
}

func TestFlowWorkflowCopySendsClientSeeds(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("XKxtXb", []any{
		[]any{flowResOtherFlow, flowResFolder, nil, []any{"Orange fox", nil, false, false, flowTestOp}, flowResProject},
		[]any{[]any{flowTestOp, flowResProject, flowResOtherFlow, nil, nil, nil, nil, []any{harness.fixture.link("video")}}},
	})

	response := harness.ok("workflows", "POST", flowResWorkflow+":copy", `{"collectionId":"`+flowResFolder+`"}`, 201)

	args := harness.fixture.args("XKxtXb", 0)
	if len(args) != 6 || flowResJSON(t, args[:4]) != flowResJSON(t, []any{[]any{flowResWorkflow, nil, nil, nil, flowResProject}, flowResProject, flowResFolder, nil}) {
		t.Fatalf("args=%v", args)
	}
	for _, seed := range args[4:] {
		if value, ok := seed.(string); !ok || !flowUUIDPattern.MatchString(value) {
			t.Fatalf("seed=%v", seed)
		}
	}
	copied := flowResDecode[flowWorkflowResource](t, response)
	if copied.ID != flowResOtherFlow || copied.ProjectID != flowResProject || flowResJSON(t, copied.MediaIDs) != `["`+flowTestOp+`"]` {
		t.Fatalf("copied=%+v", copied)
	}
}

func TestFlowWorkflowCopyBetweenProjectsReadsTheSourceProject(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.setContentsQueue(flowResContents(harness.fixture), flowResContentsOf(harness.fixture, flowResOtherProj))
	harness.reply("XKxtXb", []any{[]any{flowResOtherFlow, nil, nil, []any{"Orange fox"}, flowResProject}, []any{}})

	harness.ok("workflows", "POST", flowResWorkflow+":copy", `{"sourceProjectId":"`+flowResOtherProj+`"}`, 201)

	args := harness.fixture.args("XKxtXb", 0)
	if flowResJSON(t, args[:3]) != flowResJSON(t, []any{[]any{flowResWorkflow, nil, nil, nil, flowResOtherProj}, flowResProject, nil}) ||
		harness.fixture.count("Zzl0ze") != 2 {
		t.Fatalf("args=%v reads=%d", args, harness.fixture.count("Zzl0ze"))
	}
	harness.setContents(flowResContents(harness.fixture))
	harness.rejected("workflows", "POST", flowResOtherFlow+":copy", `{}`, 404, "flow_workflow_not_found")
	harness.rejected("workflows", "POST", flowResWorkflow+":copy", `{"sourceProjectId":"nope"}`, 400, "flow_workflow_request_invalid")
	harness.rejected("workflows", "POST", flowResWorkflow+":copy", `{"collectionId":"`+flowResOtherProj+`"}`, 404, "flow_collection_not_found")
}

func TestFlowWorkflowTrimSendsVideoOffsets(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("iVqlKd", []any{flowTestMedia})

	response := harness.ok("workflows", "POST", flowResWorkflow+":trim", `{"mediaId":"`+flowTestMedia+`","startSeconds":1,"endSeconds":4.25}`, 200)

	harness.wantArgs("iVqlKd", []any{flowTestMedia, []any{1}, []any{4, 250000000}})
	if result := flowResDecode[flowWorkflowTrimResult](t, response); result.WorkflowID != flowResWorkflow || result.MediaID != flowTestMedia || result.StartSeconds != 1 || result.EndSeconds != 4.25 {
		t.Fatalf("result=%+v", result)
	}
}

func TestFlowWorkflowTrimRejectsBadRangesAndForeignMedia(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.rejected("workflows", "POST", flowResWorkflow+":trim", `{"mediaId":"`+flowTestMedia+`","startSeconds":4,"endSeconds":4}`, 400, "flow_trim_range_invalid")
	harness.rejected("workflows", "POST", flowResWorkflow+":trim", `{"mediaId":"`+flowTestMedia+`","startSeconds":-1,"endSeconds":4}`, 400, "flow_trim_range_invalid")
	harness.rejected("workflows", "POST", flowResWorkflow+":trim", `{"mediaId":"`+flowTestMedia+`","endSeconds":4}`, 400, "flow_trim_range_invalid")
	harness.rejected("workflows", "POST", flowResWorkflow+":trim", `{"mediaId":"other-media","startSeconds":1,"endSeconds":4}`, 404, "flow_media_not_found")
	harness.rejected("workflows", "POST", flowResOldFlow+":trim", `{"mediaId":"`+flowTestMedia+`","startSeconds":1,"endSeconds":4}`, 404, "flow_media_not_found")
	harness.wantNoCall("iVqlKd")
}

func TestFlowWorkflowBatchArchiveUsesOneBatchRequest(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("pGCYOe", []any{[]any{}})

	response := harness.ok("workflows", "POST", ":batchArchive", `{"workflowIds":["`+flowResWorkflow+`","`+flowResOldFlow+`"],"archived":true}`, 200)

	harness.wantArgs("pGCYOe", []any{
		[]any{
			[]any{flowResWorkflow, nil, nil, []any{nil, nil, true}, flowResProject},
			[]any{flowResOldFlow, nil, nil, []any{nil, nil, true}, flowResProject},
		},
		[]any{[]any{"metadata.archived"}},
	})
	if result := flowResDecode[flowWorkflowBatchResult](t, response); !result.Archived || len(result.WorkflowIDs) != 2 {
		t.Fatalf("result=%+v", result)
	}
}

func TestFlowWorkflowBatchArchiveRejectsBadInputBeforeUpstream(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.rejected("workflows", "POST", ":batchArchive", `{"workflowIds":["`+flowResWorkflow+`"]}`, 400, "flow_resource_ids_invalid")
	harness.rejected("workflows", "POST", ":batchArchive", `{"archived":true}`, 400, "flow_resource_ids_invalid")
	harness.rejected("workflows", "POST", ":batchArchive", `{"workflowIds":["`+flowResOtherFlow+`"],"archived":true}`, 404, "flow_workflow_not_found")
	harness.rejected("workflows", "POST", ":batchArchive", `{"workflowIds":["`+flowResWorkflow+`","`+flowResWorkflow+`"],"archived":true}`, 400, "flow_resource_ids_invalid")
	harness.wantNoCall("pGCYOe")
}

func TestFlowWorkflowRoutesRejectUnknownShapes(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.rejected("workflows", "POST", "", "", 404, "flow_route_not_found")
	harness.rejected("workflows", "GET", "a/b", "", 404, "flow_route_not_found")
	harness.rejected("workflows", "POST", flowResWorkflow+":explode", "", 404, "flow_route_not_found")
	harness.rejected("workflows", "GET", flowResOtherFlow, "", 404, "flow_workflow_not_found")
}
