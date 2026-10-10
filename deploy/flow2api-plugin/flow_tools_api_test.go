package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

const (
	flowTestOwnedTool     = "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"
	flowTestTemplateTool  = "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb"
	flowTestCommunityTool = "cccccccc-3333-4333-8333-cccccccccccc"
	flowTestToolVersion   = "dddddddd-4444-4444-8444-dddddddddddd"
	flowTestOldVersion    = "eeeeeeee-5555-4555-8555-eeeeeeeeeeee"
	flowTestSharedTool    = "shared-fixture_1"
)

func flowToolRowFixture(id, name string, owner int, version string) []any {
	row := make([]any, 24)
	row[0], row[2], row[3], row[5] = id, name, "about "+name, version
	row[7] = []any{1700000000, 0}
	row[8] = []any{1700000100, 5}
	row[11] = "applets/" + id
	row[12] = owner
	row[14] = true
	row[15] = "https://thumb.example/" + id
	row[16] = "media-1"
	row[19] = "https://static.example/" + id
	return row
}

func flowToolDefaultRows() [][]any {
	community := flowToolRowFixture(flowTestCommunityTool, "Community", 3, flowTestToolVersion)
	community[23] = true
	return [][]any{
		flowToolRowFixture(flowTestOwnedTool, "Owned", 0, flowTestToolVersion),
		flowToolRowFixture(flowTestTemplateTool, "Template", 2, flowTestToolVersion),
		community,
	}
}

func flowToolListReply(t *testing.T, rows [][]any) string {
	t.Helper()
	return rpcEnvelope(t, "tRARke", []any{rows})
}

func flowToolFixtureService(t *testing.T, rows [][]any) (*flowFixture, *service, storageRecord) {
	t.Helper()
	fixture := newFlowFixture(t)
	service, record := flowService(t, fixture)
	fixture.reply("tRARke", flowToolListReply(t, rows))
	return fixture, service, record
}

func flowToolCall(t *testing.T, service *service, record storageRecord, shared bool, method, tail, body string) (int, string, []byte) {
	t.Helper()
	request := flowHTTPRequest{Method: method, Body: []byte(body)}
	var response httpResponse
	var err error
	if shared {
		response, err = service.flowSharedToolHTTP(t.Context(), record, tail, request)
	} else {
		response, err = service.flowToolHTTP(t.Context(), record, tail, request)
	}
	if err != nil {
		var known *publicError
		if !errors.As(err, &known) {
			t.Fatal(err)
		}
		return known.HTTPStatus, known.Code, nil
	}
	return response.StatusCode, "", response.Body
}

func TestFlowToolListDecodesNamedFields(t *testing.T) {
	// Given an account holding an owned, a template and a community tool.
	fixture, service, record := flowToolFixtureService(t, flowToolDefaultRows())

	// When the tools are listed.
	status, code, body := flowToolCall(t, service, record, false, http.MethodGet, "", "")

	// Then the wire arrays surface as named fields and ownership kinds.
	var list struct {
		Tools []flowToolResource `json:"tools"`
	}
	if err := json.Unmarshal(body, &list); err != nil || status != 200 || len(list.Tools) != 3 {
		t.Fatalf("status=%d code=%s body=%s err=%v", status, code, body, err)
	}
	owned := list.Tools[0]
	if owned.ID != flowTestOwnedTool || owned.Name != "Owned" || owned.Description != "about Owned" ||
		owned.VersionID != flowTestToolVersion || owned.CreatedAt != "2023-11-14T22:13:20Z" ||
		owned.UpdatedAt != "2023-11-14T22:15:00.000000005Z" || owned.ResourceName != "applets/"+flowTestOwnedTool ||
		owned.Owner != "owned" || !owned.Favorited || !owned.AllowRemix || owned.ThumbnailMediaID != "media-1" ||
		owned.ThumbnailURL != "https://thumb.example/"+flowTestOwnedTool || owned.StaticThumbnailURL != "https://static.example/"+flowTestOwnedTool {
		t.Fatalf("owned=%+v", owned)
	}
	if list.Tools[1].Owner != "template" || list.Tools[2].Owner != "community" || list.Tools[2].AllowRemix {
		t.Fatalf("tools=%+v", list.Tools)
	}
	if args := fixture.args("tRARke", 0); len(args) != 0 {
		t.Fatalf("args=%v", args)
	}
}

func TestFlowToolGetResolvesThroughAccountList(t *testing.T) {
	for _, scenario := range []struct {
		name, tail, code string
		status           int
	}{
		{"found", flowTestTemplateTool, "", 200},
		{"missing", "ffffffff-6666-4666-8666-ffffffffffff", "flow_tool_not_found", 404},
		{"invalid id", "bad%20id", "flow_tool_id_invalid", 400},
		{"unknown action", flowTestOwnedTool + ":unshare", "flow_route_not_found", 404},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given an account list.
			_, service, record := flowToolFixtureService(t, flowToolDefaultRows())
			// When one tool is requested.
			status, code, body := flowToolCall(t, service, record, false, http.MethodGet, scenario.tail, "")
			// Then only a listed tool is returned.
			if status != scenario.status || code != scenario.code {
				t.Fatalf("status=%d code=%s body=%s", status, code, body)
			}
		})
	}
}

func TestFlowToolUpdateSendsMaskedRowAndReadsBack(t *testing.T) {
	// Given an owned tool that the provider renames.
	fixture, service, record := flowToolFixtureService(t, flowToolDefaultRows())
	renamed := flowToolRowFixture(flowTestOwnedTool, "Renamed", 0, flowTestToolVersion)
	fixture.reply("tRARke", flowToolListReply(t, [][]any{renamed}))
	fixture.reply("sd0GXe", rpcEnvelope(t, "sd0GXe", []any{}))

	// When every editable field is patched.
	status, code, body := flowToolCall(t, service, record, false, http.MethodPatch, flowTestOwnedTool,
		`{"displayName":"  Renamed ","description":"line1\nline2","thumbnailMediaId":"media-2","staticThumbnailUrl":"https://static.example/new.png"}`)

	// Then one update carries the resource row with a field mask, and the new state is read back.
	var tool flowToolResource
	if err := json.Unmarshal(body, &tool); err != nil || status != 200 || tool.Name != "Renamed" {
		t.Fatalf("status=%d code=%s body=%s err=%v", status, code, body, err)
	}
	args := fixture.args("sd0GXe", 0)
	if fixture.count("sd0GXe") != 1 || jsonField(args, 0, 11) != "applets/"+flowTestOwnedTool || jsonField(args, 0, 2) != "Renamed" ||
		jsonField(args, 0, 3) != "line1\nline2" || jsonField(args, 0, 16) != "media-2" ||
		jsonField(args, 0, 19) != "https://static.example/new.png" || jsonField(args, 0, 5) != nil {
		t.Fatalf("args=%v", args)
	}
	for index, mask := range []string{"display_name", "description", "thumbnail_media_id", "static_thumbnail_url"} {
		if jsonField(args, 1, 0, index) != mask {
			t.Fatalf("masks=%v", jsonField(args, 1))
		}
	}
}

func TestFlowToolMutationsRequireOwnership(t *testing.T) {
	for _, scenario := range []struct {
		name, method, tail, body, rpc string
	}{
		{"update", http.MethodPatch, flowTestTemplateTool, `{"displayName":"x"}`, "sd0GXe"},
		{"delete", http.MethodDelete, flowTestTemplateTool, "", "zpwzEe"},
		{"share", http.MethodPost, flowTestCommunityTool + ":share", `{"allowRemix":true}`, "vEIlvc"},
		{"restore", http.MethodPost, flowTestTemplateTool + "/versions/" + flowTestOldVersion + ":restore", "", "CvpHmb"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given a tool the account does not own.
			fixture, service, record := flowToolFixtureService(t, flowToolDefaultRows())
			// When the caller mutates it.
			status, code, _ := flowToolCall(t, service, record, false, scenario.method, scenario.tail, scenario.body)
			// Then the mutation is refused before reaching the provider.
			if status != 403 || code != "flow_tool_not_owned" || fixture.count(scenario.rpc) != 0 {
				t.Fatalf("status=%d code=%s calls=%d", status, code, fixture.count(scenario.rpc))
			}
		})
	}
}

func TestFlowToolRejectsInvalidInputBeforeUpstream(t *testing.T) {
	for _, scenario := range []struct {
		name, method, tail, body string
		status                   int
	}{
		{"unknown field", http.MethodPatch, flowTestOwnedTool, `{"cookie":"x"}`, 400},
		{"empty patch", http.MethodPatch, flowTestOwnedTool, `{}`, 400},
		{"blank name", http.MethodPatch, flowTestOwnedTool, `{"displayName":"  "}`, 400},
		{"control name", http.MethodPatch, flowTestOwnedTool, `{"displayName":"a\u0000b"}`, 400},
		{"plain http thumbnail", http.MethodPatch, flowTestOwnedTool, `{"staticThumbnailUrl":"http://static.example/a.png"}`, 400},
		{"share without remix flag", http.MethodPost, flowTestOwnedTool + ":share", `{}`, 400},
		{"favorite with body", http.MethodPost, flowTestOwnedTool + ":favorite", `{"x":1}`, 400},
		{"wrong method", http.MethodPut, flowTestOwnedTool, `{}`, 404},
		{"nested garbage", http.MethodGet, flowTestOwnedTool + "/other", "", 404},
		{"root post", http.MethodPost, "", `{}`, 404},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given malformed input.
			fixture, service, record := flowToolFixtureService(t, flowToolDefaultRows())
			// When it reaches the tools handler.
			status, _, body := flowToolCall(t, service, record, false, scenario.method, scenario.tail, scenario.body)
			// Then it is rejected without any upstream call.
			if status != scenario.status || fixture.count("tRARke") != 0 {
				t.Fatalf("status=%d calls=%d body=%s", status, fixture.count("tRARke"), body)
			}
		})
	}
}

func TestFlowToolDeleteOwned(t *testing.T) {
	// Given an owned tool.
	fixture, service, record := flowToolFixtureService(t, flowToolDefaultRows())
	fixture.reply("zpwzEe", rpcEnvelope(t, "zpwzEe", []any{}))
	// When it is deleted.
	status, code, _ := flowToolCall(t, service, record, false, http.MethodDelete, flowTestOwnedTool, "")
	// Then the provider deletes its applet resource name once.
	args := fixture.args("zpwzEe", 0)
	if status != 204 || fixture.count("zpwzEe") != 1 || jsonField(args, 0) != "applets/"+flowTestOwnedTool {
		t.Fatalf("status=%d code=%s args=%v", status, code, args)
	}
}

func TestFlowToolCopyAllowsTemplates(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		reply any
		copy  bool
	}{
		{"wrapped row", []any{flowToolRowFixture("copy-1", "Copy", 0, flowTestToolVersion)}, true},
		{"unrecognised reply", []any{}, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given a template and the provider's copy reply.
			fixture, service, record := flowToolFixtureService(t, flowToolDefaultRows())
			fixture.reply("VVrfbf", rpcEnvelope(t, "VVrfbf", scenario.reply))
			// When the template is copied.
			status, code, body := flowToolCall(t, service, record, false, http.MethodPost, flowTestTemplateTool+":copy", "")
			// Then the raw tool id is copied once and the committed copy is reported.
			var result struct {
				SourceID string            `json:"sourceId"`
				Tool     *flowToolResource `json:"tool"`
			}
			if err := json.Unmarshal(body, &result); err != nil || status != 201 || result.SourceID != flowTestTemplateTool ||
				(result.Tool != nil) != scenario.copy || fixture.count("VVrfbf") != 1 || jsonField(fixture.args("VVrfbf", 0), 0) != flowTestTemplateTool {
				t.Fatalf("status=%d code=%s body=%s err=%v", status, code, body, err)
			}
			if scenario.copy && result.Tool.ID != "copy-1" {
				t.Fatalf("tool=%+v", result.Tool)
			}
		})
	}
}

func TestFlowToolFavoriteAndUnfavorite(t *testing.T) {
	for _, scenario := range []struct {
		action, rpc string
		favorited   bool
	}{{"favorite", "eDee9c", true}, {"unfavorite", "TsaPHc", false}} {
		t.Run(scenario.action, func(t *testing.T) {
			// Given a template tool.
			fixture, service, record := flowToolFixtureService(t, flowToolDefaultRows())
			fixture.reply(scenario.rpc, rpcEnvelope(t, scenario.rpc, []any{}))
			// When it is (un)favorited.
			status, code, body := flowToolCall(t, service, record, false, http.MethodPost, flowTestTemplateTool+":"+scenario.action, "")
			// Then the one-of selector carries the raw tool id.
			var result struct {
				ID        string `json:"id"`
				Favorited bool   `json:"favorited"`
			}
			args := fixture.args(scenario.rpc, 0)
			if err := json.Unmarshal(body, &result); err != nil || status != 200 || result.ID != flowTestTemplateTool ||
				result.Favorited != scenario.favorited || len(args) != 1 || args[0] != flowTestTemplateTool {
				t.Fatalf("status=%d code=%s body=%s args=%v", status, code, body, args)
			}
		})
	}
}

func TestFlowToolShareUsesCurrentVersion(t *testing.T) {
	for _, scenario := range []struct {
		name, body string
		reply      any
		status     int
		disallow   bool
	}{
		{"remix allowed", `{"allowRemix":true}`, []any{"shared-new"}, 201, false},
		{"remix blocked", `{"allowRemix":false}`, []any{"shared-new"}, 201, true},
		{"missing shared id", `{"allowRemix":true}`, []any{}, 502, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given an owned tool with a current version.
			fixture, service, record := flowToolFixtureService(t, flowToolDefaultRows())
			fixture.reply("vEIlvc", rpcEnvelope(t, "vEIlvc", scenario.reply))
			// When it is shared.
			status, code, body := flowToolCall(t, service, record, false, http.MethodPost, flowTestOwnedTool+":share", scenario.body)
			// Then the share request names the tool, its version and the inverted remix flag.
			args := fixture.args("vEIlvc", 0)
			if status != scenario.status || args[0] != flowTestOwnedTool || args[1] != flowTestToolVersion || args[2] != scenario.disallow {
				t.Fatalf("status=%d code=%s body=%s args=%v", status, code, body, args)
			}
			if scenario.status == 201 && !strings.Contains(string(body), `"sharedId":"shared-new"`) {
				t.Fatalf("body=%s", body)
			}
		})
	}
}

func flowSharedFixtureService(t *testing.T) (*flowFixture, *service, storageRecord) {
	t.Helper()
	fixture := newFlowFixture(t)
	service, record := flowService(t, fixture)
	fixture.reply("qJcgMc", rpcEnvelope(t, "qJcgMc", []any{[]any{
		[]any{flowTestSharedTool, "Saved title", "Saved description", nil, "https://thumb.example/saved"},
	}}))
	return fixture, service, record
}

func TestFlowSharedToolListAndGet(t *testing.T) {
	// Given a saved shared tool and a shared version with two files.
	fixture, service, record := flowSharedFixtureService(t)
	fixture.reply("J0KDW", rpcEnvelope(t, "J0KDW", []any{
		[]any{flowTestSharedTool, "Title", "Description", nil, nil, "sharedApplets/" + flowTestSharedTool, "https://thumb.example/shared"},
		"opaque",
		[]any{[]any{"App.tsx", "text/typescript", "export const A = () => <div/>;"}, []any{"data.json", "application/json", "{}"}},
	}))

	// When the saved list and the shared tool are read.
	status, code, body := flowToolCall(t, service, record, true, http.MethodGet, "", "")
	var list struct {
		SharedTools []flowSharedToolResource `json:"sharedTools"`
	}
	if err := json.Unmarshal(body, &list); err != nil || status != 200 || len(list.SharedTools) != 1 ||
		list.SharedTools[0] != (flowSharedToolResource{ID: flowTestSharedTool, Title: "Saved title", Description: "Saved description", ThumbnailURL: "https://thumb.example/saved"}) {
		t.Fatalf("status=%d code=%s body=%s err=%v", status, code, body, err)
	}
	status, code, body = flowToolCall(t, service, record, true, http.MethodGet, flowTestSharedTool, "")

	// Then metadata and every file are returned as named fields.
	var detail struct {
		flowSharedToolResource
		Files []flowToolFile `json:"files"`
	}
	if err := json.Unmarshal(body, &detail); err != nil || status != 200 || detail.Title != "Title" || detail.Description != "Description" ||
		detail.ResourceName != "sharedApplets/"+flowTestSharedTool || detail.ThumbnailURL != "https://thumb.example/shared" ||
		len(detail.Files) != 2 || detail.Files[0].Content != "export const A = () => <div/>;" || detail.Files[1].Name != "data.json" {
		t.Fatalf("status=%d code=%s body=%s err=%v", status, code, body, err)
	}
	if jsonField(fixture.args("J0KDW", 0), 0) != "sharedApplets/"+flowTestSharedTool {
		t.Fatalf("args=%v", fixture.args("J0KDW", 0))
	}
}

func TestFlowSharedToolGetRejectsIdentityMismatch(t *testing.T) {
	// Given a provider reply for a different shared tool.
	fixture, service, record := flowSharedFixtureService(t)
	fixture.reply("J0KDW", rpcEnvelope(t, "J0KDW", []any{[]any{"other-shared", "Title"}, "", []any{}}))
	// When the caller reads one shared tool.
	status, code, _ := flowToolCall(t, service, record, true, http.MethodGet, flowTestSharedTool, "")
	// Then the mismatched data is not returned.
	if status != 502 || code != "flow_shared_tool_identity_mismatch" {
		t.Fatalf("status=%d code=%s", status, code)
	}
}

func TestFlowSharedToolDeleteRequiresSavedLink(t *testing.T) {
	for _, scenario := range []struct {
		name, id string
		status   int
		calls    int
	}{{"saved", flowTestSharedTool, 204, 1}, {"not saved", "unsaved-shared", 404, 0}} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given one saved shared tool.
			fixture, service, record := flowSharedFixtureService(t)
			fixture.reply("ipGN1", rpcEnvelope(t, "ipGN1", []any{}))
			// When a shared tool is removed.
			status, _, _ := flowToolCall(t, service, record, true, http.MethodDelete, scenario.id, "")
			// Then only a saved link reaches the provider.
			if status != scenario.status || fixture.count("ipGN1") != scenario.calls {
				t.Fatalf("status=%d calls=%d", status, fixture.count("ipGN1"))
			}
			if scenario.calls == 1 && jsonField(fixture.args("ipGN1", 0), 0) != scenario.id {
				t.Fatalf("args=%v", fixture.args("ipGN1", 0))
			}
		})
	}
}

func TestFlowSharedToolForkAndFavorite(t *testing.T) {
	// Given a shared tool and a project.
	fixture, service, record := flowSharedFixtureService(t)
	fixture.reply("CSvxid", rpcEnvelope(t, "CSvxid", []any{flowToolRowFixture("fork-1", "Fork", 0, flowTestToolVersion)}))
	fixture.reply("eDee9c", rpcEnvelope(t, "eDee9c", []any{}))
	fixture.reply("TsaPHc", rpcEnvelope(t, "TsaPHc", []any{}))

	// When it is forked into the project and (un)favorited.
	status, code, body := flowToolCall(t, service, record, true, http.MethodPost, flowTestSharedTool+":fork", `{"projectId":"`+flowTestProject+`"}`)
	var forked flowToolResource
	if err := json.Unmarshal(body, &forked); err != nil || status != 201 || forked.ID != "fork-1" || forked.Owner != "owned" {
		t.Fatalf("status=%d code=%s body=%s err=%v", status, code, body, err)
	}
	args := fixture.args("CSvxid", 0)
	if len(args) != 2 || args[0] != flowTestSharedTool || args[1] != flowTestProject {
		t.Fatalf("fork args=%v", args)
	}
	for _, action := range []struct{ name, rpc string }{{"favorite", "eDee9c"}, {"unfavorite", "TsaPHc"}} {
		status, code, _ = flowToolCall(t, service, record, true, http.MethodPost, flowTestSharedTool+":"+action.name, "")
		args = fixture.args(action.rpc, 0)

		// Then the shared id travels in the second slot of the one-of selector.
		if status != 200 || len(args) != 2 || args[0] != nil || args[1] != flowTestSharedTool {
			t.Fatalf("%s status=%d code=%s args=%v", action.name, status, code, args)
		}
	}
}

func TestFlowSharedToolRejectsUnprovenAndInvalidRoutes(t *testing.T) {
	for _, scenario := range []struct {
		name, method, tail, body, code string
		status                         int
	}{
		{"unshare requires POST", http.MethodGet, flowTestSharedTool + ":unshare", "", "flow_route_not_found", 404},
		{"fork without project", http.MethodPost, flowTestSharedTool + ":fork", `{}`, "flow_project_id_invalid", 400},
		{"fork foreign field", http.MethodPost, flowTestSharedTool + ":fork", `{"projectId":"` + flowTestProject + `","x":1}`, "flow_unsupported_generation_option", 400},
		{"invalid id", http.MethodGet, "bad%20id", "", "flow_shared_tool_id_invalid", 400},
		{"nested path", http.MethodGet, flowTestSharedTool + "/versions", "", "flow_route_not_found", 404},
		{"root delete", http.MethodDelete, "", "", "flow_route_not_found", 404},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given a shared-tool request outside the proven protocol.
			fixture, service, record := flowSharedFixtureService(t)
			// When it is handled.
			status, code, _ := flowToolCall(t, service, record, true, scenario.method, scenario.tail, scenario.body)
			// Then it fails before any provider call.
			if status != scenario.status || code != scenario.code || fixture.count("qJcgMc") != 0 {
				t.Fatalf("status=%d code=%s", status, code)
			}
		})
	}
}

func TestFlowSharedToolUnshareUsesTheSharedIdentifier(t *testing.T) {
	// Given a provider that accepts revocation for its authenticated owner.
	fixture, service, record := flowSharedFixtureService(t)
	fixture.reply("h4NnAe", rpcEnvelope(t, "h4NnAe", []any{}))
	// When the owner revokes the shared link.
	status, code, _ := flowToolCall(t, service, record, true, http.MethodPost, flowTestSharedTool+":unshare", "")
	// Then the shared identifier, not a tool resource name, is submitted once.
	if status != 204 || fixture.count("h4NnAe") != 1 || jsonField(fixture.args("h4NnAe", 0), 0) != flowTestSharedTool {
		t.Fatalf("status=%d code=%s", status, code)
	}
}
