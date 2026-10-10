package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func flowMediaAPIFixture(fixture *flowFixture) []any {
	return []any{flowTestMedia, flowTestProject, flowTestOp, nil, nil, nil, nil, []any{fixture.link("video")}}
}

func TestFlowMediaDownloadUsesTheAuthenticatedRecord(t *testing.T) {
	// Given a media record returned by the configured account.
	fixture := newFlowFixture(t)
	service, _ := flowService(t, fixture)
	fixture.reply("as29s", rpcEnvelope(t, "as29s", flowMediaAPIFixture(fixture)))
	// When the API consumer downloads it.
	response := flowHTTPTest(t, service, flowHTTPRequest{
		Method: "GET", Path: "/v1/flow/projects/" + flowTestProject + "/media/" + flowTestMedia + ":download", CallerScope: strings.Repeat("a", 64),
	})
	// Then actual media bytes are returned, not a workflow id or a placeholder.
	if response.StatusCode != http.StatusOK || response.Headers.Get("Content-Type") != "video/mp4" || !slices.Equal(response.Body, flowTestMP4) {
		t.Fatalf("response status=%d mime=%s body=%s", response.StatusCode, response.Headers.Get("Content-Type"), response.Body)
	}
}

func TestFlowMediaRejectsAnotherProjectBeforeMutation(t *testing.T) {
	// Given an upstream record belonging to a different project.
	fixture := newFlowFixture(t)
	service, _ := flowService(t, fixture)
	media := flowMediaAPIFixture(fixture)
	media[1] = flowTestOp
	fixture.reply("as29s", rpcEnvelope(t, "as29s", media))
	// When a caller tries to remove it through this project's path.
	response := flowHTTPTest(t, service, flowHTTPRequest{
		Method: "DELETE", Path: "/v1/flow/projects/" + flowTestProject + "/media/" + flowTestMedia, CallerScope: strings.Repeat("a", 64),
	})
	// Then project identity fails and no archive request is sent.
	if response.StatusCode != 502 || fixture.count("pGCYOe") != 0 {
		t.Fatalf("status=%d archive calls=%d", response.StatusCode, fixture.count("pGCYOe"))
	}
}

func TestFlowMediaTrashAndRestoreUpdateOnlyArchiveState(t *testing.T) {
	for _, archived := range []bool{true, false} {
		t.Run(map[bool]string{true: "trash", false: "restore"}[archived], func(t *testing.T) {
			// Given an existing video and its workflow.
			fixture := newFlowFixture(t)
			service, _ := flowService(t, fixture)
			fixture.reply("as29s", rpcEnvelope(t, "as29s", flowMediaAPIFixture(fixture)))
			fixture.reply("pGCYOe", rpcEnvelope(t, "pGCYOe", []any{}))
			request := flowHTTPRequest{Method: "DELETE", Path: "/v1/flow/projects/" + flowTestProject + "/media/" + flowTestMedia, CallerScope: strings.Repeat("a", 64)}
			if !archived {
				request.Method, request.Path = "POST", request.Path+":restore"
			}
			// When the caller moves or restores that media.
			response := flowHTTPTest(t, service, request)
			// Then the operation preserves collection and other workflow fields.
			args := fixture.args("pGCYOe", 0)
			if response.StatusCode != 204 || jsonField(args, 0, 0, 0) != flowTestOp ||
				jsonField(args, 0, 0, 3, 2) != archived || jsonField(args, 1, 0, 0) != "metadata.archived" {
				t.Fatalf("status=%d args=%v", response.StatusCode, args)
			}
		})
	}
}

func TestFlowMediaListFiltersByWorkflowArchiveAndName(t *testing.T) {
	// Given a project containing a trashed uploaded video.
	fixture := newFlowFixture(t)
	service, _ := flowService(t, fixture)
	fixture.reply("Zzl0ze", rpcEnvelope(t, "Zzl0ze", []any{
		nil, []any{[]any{flowTestOp, nil, nil, []any{"Orange fox", nil, true}, flowTestProject}},
		[]any{flowMediaAPIFixture(fixture)},
	}))
	// When the caller searches the project's trash.
	response := flowHTTPTest(t, service, flowHTTPRequest{
		Method: "GET", Path: "/v1/flow/projects/" + flowTestProject + "/media", CallerScope: strings.Repeat("a", 64),
		Query: url.Values{"archived": {"true"}, "search": {"FOX"}, "type": {"video"}},
	})
	// Then the media id, not the workflow id, is returned with its name and state.
	var result struct {
		Media []flowMediaResource `json:"media"`
	}
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || len(result.Media) != 1 || result.Media[0].ID != flowTestMedia || result.Media[0].Archived == nil || !*result.Media[0].Archived || result.Media[0].Title != "Orange fox" {
		t.Fatalf("response=%s", response.Body)
	}
}

func TestFlowMediaGetDoesNotInventArchiveState(t *testing.T) {
	// Given a media response without its workflow's archive metadata.
	fixture := newFlowFixture(t)
	service, _ := flowService(t, fixture)
	fixture.reply("as29s", rpcEnvelope(t, "as29s", flowMediaAPIFixture(fixture)))
	// When the caller retrieves only that media record.
	response := flowHTTPTest(t, service, flowHTTPRequest{
		Method: "GET", Path: "/v1/flow/projects/" + flowTestProject + "/media/" + flowTestMedia, CallerScope: strings.Repeat("a", 64),
	})
	// Then unknown state is absent, rather than reported as not trashed.
	var body map[string]json.RawMessage
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatal(err)
	}
	if _, present := body["archived"]; present || response.StatusCode != 200 {
		t.Fatalf("response=%s", response.Body)
	}
}
