package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
)

const (
	flowResProject    = flowTestProject
	flowResFolder     = "cccccccc-1111-4222-8333-444444444444"
	flowResChild      = "cccccccc-2222-4222-8333-444444444444"
	flowResTrash      = "cccccccc-3333-4222-8333-444444444444"
	flowResNew        = "cccccccc-9999-4222-8333-444444444444"
	flowResScene      = "dddddddd-1111-4222-8333-444444444444"
	flowResFiled      = "dddddddd-2222-4222-8333-444444444444"
	flowResSceneCopy  = "dddddddd-3333-4222-8333-444444444444"
	flowResWorkflow   = flowTestOp
	flowResOldFlow    = "bbbbbbbb-2222-4ddd-8eee-ffffffffffff"
	flowResOtherFlow  = "bbbbbbbb-3333-4ddd-8eee-ffffffffffff"
	flowResOtherProj  = "99999999-2222-4333-8444-555555555555"
	flowResPrimaryOld = "media-of-the-old-workflow"
)

type flowResHarness struct {
	t       *testing.T
	fixture *flowFixture
	service *service
	record  storageRecord
}

func flowResContents(fixture *flowFixture) []any {
	return flowResContentsOf(fixture, flowResProject)
}

func flowResContentsOf(fixture *flowFixture, project string) []any {
	return []any{
		[]any{
			[]any{flowResFolder, nil, []any{"Folder", nil, false, nil, false}, project},
			[]any{flowResChild, flowResFolder, []any{"Child", nil, true}, project},
			[]any{flowResTrash, nil, []any{"Trash", nil, true}, project},
		},
		[]any{
			[]any{flowResWorkflow, flowResFolder, nil, []any{"Orange fox", []any{"1700000000", 5}, false, false, flowTestMedia}, project},
			[]any{flowResOldFlow, nil, nil, []any{"Old take", nil, true, false, flowResPrimaryOld}, project},
		},
		[]any{[]any{flowTestMedia, project, flowResWorkflow, nil, nil, nil, nil, []any{fixture.link("video")}}},
		nil,
		[]any{
			[]any{flowResScene, "Opening", nil, nil, nil, float64(2), nil, []any{}},
			[]any{flowResFiled, "Filed away", nil, nil, nil, float64(1), flowResTrash, []any{true}},
		},
		[]any{},
		nil,
	}
}

func newFlowResHarness(t *testing.T) *flowResHarness {
	t.Helper()
	fixture := newFlowFixture(t)
	service, record := flowService(t, fixture)
	harness := &flowResHarness{t: t, fixture: fixture, service: service, record: record}
	harness.setContents(flowResContents(fixture))
	return harness
}

func (harness *flowResHarness) setContents(contents any) {
	harness.fixture.mu.Lock()
	defer harness.fixture.mu.Unlock()
	harness.fixture.replies["Zzl0ze"] = []string{rpcEnvelope(harness.t, "Zzl0ze", contents)}
}

// setContentsQueue serves each contents read in order, ending on the last entry.
func (harness *flowResHarness) setContentsQueue(contents ...any) {
	queue := make([]string, len(contents))
	for index, item := range contents {
		queue[index] = rpcEnvelope(harness.t, "Zzl0ze", item)
	}
	harness.fixture.mu.Lock()
	defer harness.fixture.mu.Unlock()
	harness.fixture.replies["Zzl0ze"] = queue
}

func (harness *flowResHarness) reply(rpcID string, payload any) {
	harness.fixture.reply(rpcID, rpcEnvelope(harness.t, rpcID, payload))
}

func (harness *flowResHarness) call(kind, method, tail, body string, query url.Values) (httpResponse, *publicError) {
	harness.t.Helper()
	request := flowHTTPRequest{Method: method, Body: []byte(body), Query: query}
	var response httpResponse
	var err error
	switch kind {
	case "collections":
		response, err = harness.service.flowCollectionHTTP(context.Background(), harness.record, flowResProject, tail, request)
	case "scenes":
		response, err = harness.service.flowSceneHTTP(context.Background(), harness.record, flowResProject, tail, request)
	case "workflows":
		response, err = harness.service.flowWorkflowHTTP(context.Background(), harness.record, flowResProject, tail, request)
	default:
		harness.t.Fatalf("unknown resource kind %s", kind)
	}
	if err == nil {
		return response, nil
	}
	var public *publicError
	if !errors.As(err, &public) {
		harness.t.Fatalf("unexpected non-public error: %v", err)
	}
	return response, public
}

func (harness *flowResHarness) ok(kind, method, tail, body string, status int) httpResponse {
	harness.t.Helper()
	response, failure := harness.call(kind, method, tail, body, nil)
	if failure != nil || response.StatusCode != status {
		harness.t.Fatalf("%s %s %s: status=%d failure=%+v body=%s", kind, method, tail, response.StatusCode, failure, response.Body)
	}
	return response
}

func (harness *flowResHarness) rejected(kind, method, tail, body string, status int, code string) {
	harness.t.Helper()
	_, failure := harness.call(kind, method, tail, body, nil)
	if failure == nil || failure.HTTPStatus != status || failure.Code != code {
		harness.t.Fatalf("%s %s %s: failure=%+v, want %d %s", kind, method, tail, failure, status, code)
	}
}

func (harness *flowResHarness) wantArgs(rpcID string, want any) {
	harness.t.Helper()
	got, expected := flowResJSON(harness.t, harness.fixture.args(rpcID, 0)), flowResJSON(harness.t, want)
	if got != expected {
		harness.t.Fatalf("%s args = %s, want %s", rpcID, got, expected)
	}
}

func (harness *flowResHarness) wantNoCall(rpcIDs ...string) {
	harness.t.Helper()
	for _, rpcID := range rpcIDs {
		if harness.fixture.count(rpcID) != 0 {
			harness.t.Fatalf("%s was called", rpcID)
		}
	}
}

func flowResJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func flowResDecode[T any](t *testing.T, response httpResponse) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(response.Body, &value); err != nil {
		t.Fatalf("body %s: %v", response.Body, err)
	}
	return value
}

func flowResCollectionIDs(list flowCollectionList) []string {
	ids := []string{}
	for _, collection := range list.Collections {
		ids = append(ids, collection.ID)
	}
	return ids
}

func TestFlowCollectionListFiltersTrashParentAndName(t *testing.T) {
	harness := newFlowResHarness(t)
	for _, scenario := range []struct {
		query url.Values
		want  string
	}{
		{nil, `["` + flowResFolder + `"]`},
		{url.Values{"archived": {"true"}}, `["` + flowResChild + `","` + flowResTrash + `"]`},
		{url.Values{"archived": {"true"}, "parentId": {flowResFolder}}, `["` + flowResChild + `"]`},
		{url.Values{"search": {"FOLD"}}, `["` + flowResFolder + `"]`},
		{url.Values{"parentId": {""}}, `["` + flowResFolder + `"]`},
	} {
		response, failure := harness.call("collections", "GET", "", "", scenario.query)
		if failure != nil || response.StatusCode != 200 {
			t.Fatalf("query %v: failure=%+v", scenario.query, failure)
		}
		if got := flowResJSON(t, flowResCollectionIDs(flowResDecode[flowCollectionList](t, response))); got != scenario.want {
			t.Fatalf("query %v: ids=%s want %s", scenario.query, got, scenario.want)
		}
	}
	_, failure := harness.call("collections", "GET", "", "", url.Values{"archived": {"maybe"}})
	if failure == nil || failure.Code != "flow_archived_filter_invalid" {
		t.Fatalf("failure=%+v", failure)
	}
}

func TestFlowCollectionCreateSendsTheCapturedWire(t *testing.T) {
	original := newFlowCollectionID
	newFlowCollectionID = func() string { return flowResNew }
	t.Cleanup(func() { newFlowCollectionID = original })
	harness := newFlowResHarness(t)
	harness.reply("Uxbujd", []any{flowResNew, flowResFolder, []any{"Fresh"}, flowResProject})

	response := harness.ok("collections", "POST", "", `{"title":" Fresh ","parentId":"`+flowResFolder+`"}`, 201)

	harness.wantArgs("Uxbujd", []any{"projects/" + flowResProject, []any{flowResNew, flowResFolder, []any{"Fresh"}, flowResProject}})
	created := flowResDecode[flowCollectionResource](t, response)
	if created.ID != flowResNew || created.ParentID != flowResFolder || created.Title != "Fresh" || created.ProjectID != flowResProject {
		t.Fatalf("created=%+v", created)
	}
}

func TestFlowCollectionCreateAtRootSendsNoParent(t *testing.T) {
	original := newFlowCollectionID
	newFlowCollectionID = func() string { return flowResNew }
	t.Cleanup(func() { newFlowCollectionID = original })
	harness := newFlowResHarness(t)
	harness.reply("Uxbujd", []any{flowResNew, nil, []any{"Fresh"}, flowResProject})

	harness.ok("collections", "POST", "", `{"title":"Fresh"}`, 201)

	harness.wantArgs("Uxbujd", []any{"projects/" + flowResProject, []any{flowResNew, nil, []any{"Fresh"}, flowResProject}})
}

func TestFlowCollectionCreateRejectsBadInputBeforeUpstream(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.rejected("collections", "POST", "", `{"title":" "}`, 400, "flow_title_invalid")
	harness.rejected("collections", "POST", "", `{"title":"x","rpc":"Uxbujd"}`, 400, "flow_unsupported_generation_option")
	harness.rejected("collections", "POST", "", `{"title":"x","parentId":"`+flowResOtherProj+`"}`, 404, "flow_collection_not_found")
	harness.wantNoCall("Uxbujd")
}

func TestFlowCollectionCreateRejectsAnotherUpstreamRecord(t *testing.T) {
	original := newFlowCollectionID
	newFlowCollectionID = func() string { return flowResNew }
	t.Cleanup(func() { newFlowCollectionID = original })
	for name, record := range map[string][]any{
		"other id":      {flowResFolder, nil, []any{"Fresh"}, flowResProject},
		"other project": {flowResNew, nil, []any{"Fresh"}, flowResOtherProj},
	} {
		t.Run(name, func(t *testing.T) {
			harness := newFlowResHarness(t)
			harness.reply("Uxbujd", record)
			harness.rejected("collections", "POST", "", `{"title":"Fresh"}`, 502, "flow_collection_identity_mismatch")
		})
	}
}

func TestFlowCollectionUpdateSendsOnlyTheChangedFields(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("O2COMe", []any{flowResFolder, nil, []any{"Renamed", nil, false, nil, true}, flowResProject})

	response := harness.ok("collections", "PATCH", flowResFolder, `{"title":"Renamed","favorited":true}`, 200)

	harness.wantArgs("O2COMe", []any{
		[]any{flowResFolder, nil, []any{"Renamed", nil, nil, nil, true}, flowResProject},
		[]any{[]any{"metadata.display_name", "metadata.favorited"}},
	})
	if updated := flowResDecode[flowCollectionResource](t, response); updated.Title != "Renamed" || !updated.Favorited {
		t.Fatalf("updated=%+v", updated)
	}
}

func TestFlowCollectionMovesToTheProjectRootWithAnEmptyParent(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("O2COMe", []any{flowResChild, nil, []any{"Child", nil, true}, flowResProject})

	harness.ok("collections", "PATCH", flowResChild, `{"parentId":""}`, 200)

	harness.wantArgs("O2COMe", []any{
		[]any{flowResChild, "", nil, flowResProject}, []any{[]any{"parent_collection_id"}},
	})
}

func TestFlowCollectionUpdateRefusesInvalidChangesBeforeUpstream(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.rejected("collections", "PATCH", flowResFolder, `{}`, 400, "flow_collection_request_invalid")
	harness.rejected("collections", "PATCH", flowResFolder, `{"parentId":"`+flowResChild+`"}`, 409, "flow_collection_cycle")
	harness.rejected("collections", "PATCH", flowResFolder, `{"parentId":"`+flowResFolder+`"}`, 409, "flow_collection_cycle")
	harness.rejected("collections", "PATCH", flowResFolder, `{"parentId":"`+flowResOtherProj+`"}`, 404, "flow_collection_not_found")
	harness.rejected("collections", "PATCH", flowResFolder, `{"id":"x"}`, 400, "flow_unsupported_generation_option")
	harness.rejected("collections", "PATCH", flowResOtherProj, `{"title":"x"}`, 404, "flow_collection_not_found")
	harness.wantNoCall("O2COMe")
}

func TestFlowCollectionTrashAndRestoreOnlyChangeTheArchiveFlag(t *testing.T) {
	for _, archived := range []bool{true, false} {
		harness := newFlowResHarness(t)
		harness.reply("O2COMe", []any{flowResFolder, nil, []any{"Folder", nil, archived}, flowResProject})
		if archived {
			harness.ok("collections", "DELETE", flowResFolder, "", 204)
		} else {
			harness.ok("collections", "POST", flowResFolder+":restore", "", 200)
		}
		harness.wantArgs("O2COMe", []any{
			[]any{flowResFolder, nil, []any{nil, nil, archived}, flowResProject}, []any{[]any{"metadata.archived"}},
		})
	}
}

func TestFlowCollectionPurgeDeletesOnlyEmptyTrashedCollections(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.rejected("collections", "POST", flowResFolder+":purge", "", 409, "flow_collection_not_archived")
	harness.rejected("collections", "POST", flowResTrash+":purge", "", 409, "flow_collection_not_empty")
	harness.wantNoCall("cz8Z4b")
	harness.reply("cz8Z4b", []any{})

	harness.ok("collections", "POST", flowResChild+":purge", "", 204)

	harness.wantArgs("cz8Z4b", []any{[]any{flowResChild}, nil, flowResProject})
}

func TestFlowCollectionItemsMoveIntoAndOutOfACollection(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("kVdhHf", []any{[]any{}})
	body := `{"workflowIds":["` + flowResWorkflow + `"],"collectionIds":["` + flowResChild + `"],"sceneIds":["` + flowResScene + `"]}`

	response := harness.ok("collections", "POST", flowResFolder+":addItems", body, 200)

	harness.wantArgs("kVdhHf", []any{
		flowResProject, []any{flowResFolder}, []any{flowResWorkflow}, []any{flowResChild}, nil, nil, nil, []any{flowResScene},
	})
	moved := flowResDecode[flowCollectionMembership](t, response)
	if moved.CollectionID != flowResFolder || len(moved.WorkflowIDs) != 1 || len(moved.SceneIDs) != 1 || len(moved.CollectionIDs) != 1 {
		t.Fatalf("moved=%+v", moved)
	}
}

func TestFlowCollectionItemsMoveToTheRootWithTheEmptyMarker(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.reply("kVdhHf", []any{[]any{}})

	harness.ok("collections", "POST", flowResFolder+":removeItems", `{"workflowIds":["`+flowResWorkflow+`"]}`, 200)

	harness.wantArgs("kVdhHf", []any{flowResProject, nil, []any{flowResWorkflow}, nil, nil, []any{}})
}

func TestFlowCollectionItemsRejectUnknownOrCyclicMembers(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.rejected("collections", "POST", flowResChild+":addItems", `{"collectionIds":["`+flowResFolder+`"]}`, 409, "flow_collection_cycle")
	harness.rejected("collections", "POST", flowResFolder+":addItems", `{"workflowIds":["`+flowResOtherFlow+`"]}`, 404, "flow_workflow_not_found")
	harness.rejected("collections", "POST", flowResFolder+":addItems", `{"sceneIds":["`+flowResWorkflow+`"]}`, 404, "flow_scene_not_found")
	harness.rejected("collections", "POST", flowResFolder+":addItems", `{}`, 400, "flow_resource_ids_invalid")
	harness.rejected("collections", "POST", flowResFolder+":addItems", `{"workflowIds":["`+flowResWorkflow+`","`+flowResWorkflow+`"]}`, 400, "flow_resource_ids_invalid")
	harness.wantNoCall("kVdhHf")
}

func TestFlowCollectionRejectsRecordsOfAnotherProject(t *testing.T) {
	harness := newFlowResHarness(t)
	contents := flowResContents(harness.fixture)
	contents[0] = []any{[]any{flowResFolder, nil, []any{"Folder"}, flowResOtherProj}}
	harness.setContents(contents)
	harness.rejected("collections", "GET", flowResFolder, "", 502, "flow_collection_identity_mismatch")
	harness.rejected("collections", "PATCH", flowResFolder, `{"title":"x"}`, 502, "flow_collection_identity_mismatch")
	harness.wantNoCall("O2COMe")
}

func TestFlowCollectionRoutesRejectUnknownShapes(t *testing.T) {
	harness := newFlowResHarness(t)
	harness.rejected("collections", "PUT", "", "", 404, "flow_route_not_found")
	harness.rejected("collections", "GET", "a/b", "", 404, "flow_route_not_found")
	harness.rejected("collections", "POST", flowResFolder+":explode", "", 404, "flow_route_not_found")
	harness.rejected("collections", "GET", flowResOtherProj, "", 404, "flow_collection_not_found")
}
