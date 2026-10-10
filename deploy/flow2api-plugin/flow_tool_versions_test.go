package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const flowTestThirdVersion = "ffffffff-6666-4666-8666-ffffffffffff"

func flowMessageFixture(text, role, thought, id string) []any {
	message := make([]any, 10)
	message[0], message[1], message[6], message[9] = text, role, thought, id
	return message
}

func flowVersionPayloadFixture(toolID, versionID string, messages, files []any) []any {
	meta := make([]any, 9)
	meta[0], meta[1], meta[4] = toolID, versionID, "version description"
	meta[7] = []any{1700000000, 0}
	meta[8] = "applets/" + toolID + "/versions/" + versionID
	return []any{meta, "changes text", []any{"opaque-session", messages}, files}
}

func TestFlowToolVersionsComeFromSnapshotMarkers(t *testing.T) {
	// Given a template tool whose history marks three snapshots, one repeated and one the current version.
	fixture, service, record := flowToolFixtureService(t, flowToolDefaultRows())
	fixture.reply("ZLaYcd", rpcEnvelope(t, "ZLaYcd", flowVersionPayloadFixture(flowTestTemplateTool, flowTestToolVersion, []any{
		flowMessageFixture("[VERSION_SNAPSHOT] version="+flowTestThirdVersion, "system", "", "m1"),
		flowMessageFixture("[STATE_UPDATE] TITLE=Counter", "system", "", "m2"),
		flowMessageFixture("[VERSION_SNAPSHOT] version="+flowTestOldVersion, "system", "", "m3"),
		flowMessageFixture("[VERSION_SNAPSHOT] version="+flowTestThirdVersion, "system", "", "m4"),
		flowMessageFixture("[VERSION_SNAPSHOT] version="+flowTestToolVersion, "system", "", "m5"),
		flowMessageFixture("user text mentioning [VERSION_SNAPSHOT] version="+flowTestOwnedTool, "user", "", "m6"),
	}, []any{})))

	// When the versions are listed.
	status, code, body := flowToolCall(t, service, record, false, http.MethodGet, flowTestTemplateTool+"/versions", "")

	// Then each marker appears once in history order and the current version closes the list.
	var list struct {
		ToolID   string               `json:"toolId"`
		Versions []flowToolVersionRef `json:"versions"`
	}
	want := []flowToolVersionRef{{ID: flowTestThirdVersion}, {ID: flowTestOldVersion}, {ID: flowTestToolVersion, Current: true}}
	if err := json.Unmarshal(body, &list); err != nil || status != 200 || list.ToolID != flowTestTemplateTool || len(list.Versions) != len(want) {
		t.Fatalf("status=%d code=%s body=%s err=%v", status, code, body, err)
	}
	for index := range want {
		if list.Versions[index] != want[index] {
			t.Fatalf("versions=%+v", list.Versions)
		}
	}
	if jsonField(fixture.args("ZLaYcd", 0), 0) != "applets/"+flowTestTemplateTool+"/versions/"+flowTestToolVersion {
		t.Fatalf("args=%v", fixture.args("ZLaYcd", 0))
	}
}

func TestFlowToolVersionsEmptyWithoutCurrentVersion(t *testing.T) {
	// Given a tool that has no current version id.
	_, service, record := flowToolFixtureService(t, [][]any{flowToolRowFixture(flowTestOwnedTool, "Owned", 0, "")})
	// When its versions are listed.
	status, _, body := flowToolCall(t, service, record, false, http.MethodGet, flowTestOwnedTool+"/versions", "")
	// Then the list is empty and no version read is attempted.
	if status != 200 || !strings.Contains(string(body), `"versions":[]`) {
		t.Fatalf("status=%d body=%s", status, body)
	}
}

func TestFlowToolVersionPreservesFilesAndMessages(t *testing.T) {
	// Given a version with a large TSX file, a second file and system/user messages.
	fixture, service, record := flowToolFixtureService(t, flowToolDefaultRows())
	source := strings.Repeat("const 값 = '💡'; // <div>&</div>\n", 6000)
	fixture.reply("ZLaYcd", rpcEnvelope(t, "ZLaYcd", flowVersionPayloadFixture(flowTestOwnedTool, flowTestOldVersion, []any{
		flowMessageFixture("[STATE_UPDATE] TITLE=Counter", "system", "", "m1"),
		flowMessageFixture("answer", "app_builder", "trace", "m2"),
	}, []any{
		[]any{"App.tsx", "text/typescript", source},
		[]any{"index.css", "text/css", "body{}"},
	})))

	// When that version is read.
	status, code, body := flowToolCall(t, service, record, false, http.MethodGet, flowTestOwnedTool+"/versions/"+flowTestOldVersion, "")

	// Then metadata, history and every file survive without truncation.
	var version flowToolVersion
	if err := json.Unmarshal(body, &version); err != nil || status != 200 {
		t.Fatalf("status=%d code=%s err=%v", status, code, err)
	}
	if version.ToolID != flowTestOwnedTool || version.VersionID != flowTestOldVersion || version.Description != "version description" ||
		version.CreatedAt != "2023-11-14T22:13:20Z" || version.ResourceName != "applets/"+flowTestOwnedTool+"/versions/"+flowTestOldVersion ||
		version.Changes != "changes text" {
		t.Fatalf("version=%+v", version)
	}
	if len(version.Files) != 2 || version.Files[0].Name != "App.tsx" || version.Files[0].MimeType != "text/typescript" ||
		version.Files[0].Content != source || version.Files[1].Content != "body{}" {
		t.Fatalf("files=%d", len(version.Files))
	}
	if len(version.Messages) != 2 || version.Messages[0] != (flowToolMessage{ID: "m1", Role: "system", Text: "[STATE_UPDATE] TITLE=Counter"}) ||
		version.Messages[1] != (flowToolMessage{ID: "m2", Role: "app_builder", Text: "answer", Thought: "trace"}) {
		t.Fatalf("messages=%+v", version.Messages)
	}
	if strings.Contains(string(body), "opaque-session") {
		t.Fatalf("opaque session leaked: %.200s", body)
	}
}

func TestFlowToolVersionRejectsMismatchedProviderData(t *testing.T) {
	for _, scenario := range []struct {
		name, code string
		payload    []any
	}{
		{"wrong version", "flow_tool_version_identity_mismatch", flowVersionPayloadFixture(flowTestOwnedTool, flowTestThirdVersion, []any{}, []any{})},
		{"wrong tool", "flow_tool_version_identity_mismatch", flowVersionPayloadFixture(flowTestTemplateTool, flowTestOldVersion, []any{}, []any{})},
		{"unnamed file", "flow_tool_files_invalid", flowVersionPayloadFixture(flowTestOwnedTool, flowTestOldVersion, []any{}, []any{[]any{"", "text/plain", "x"}})},
		{"binary content", "flow_tool_files_invalid", flowVersionPayloadFixture(flowTestOwnedTool, flowTestOldVersion, []any{}, []any{[]any{"a.bin", "x/y", 5}})},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given a provider reply that does not match the request.
			fixture, service, record := flowToolFixtureService(t, flowToolDefaultRows())
			fixture.reply("ZLaYcd", rpcEnvelope(t, "ZLaYcd", scenario.payload))
			// When the version is read.
			status, code, _ := flowToolCall(t, service, record, false, http.MethodGet, flowTestOwnedTool+"/versions/"+flowTestOldVersion, "")
			// Then a protocol error is returned instead of data.
			if status != 502 || code != scenario.code {
				t.Fatalf("status=%d code=%s", status, code)
			}
		})
	}
}

func TestFlowToolVersionRoutesRejectInvalidRequests(t *testing.T) {
	for _, scenario := range []struct {
		name, method, tail string
		status             int
	}{
		{"dotted version", http.MethodGet, flowTestOwnedTool + "/versions/bad.version", 400},
		{"read restore route", http.MethodGet, flowTestOwnedTool + "/versions/" + flowTestOldVersion + ":restore", 404},
		{"post version", http.MethodPost, flowTestOwnedTool + "/versions/" + flowTestOldVersion, 404},
		{"unknown version action", http.MethodPost, flowTestOwnedTool + "/versions/" + flowTestOldVersion + ":delete", 404},
		{"post versions list", http.MethodPost, flowTestOwnedTool + "/versions", 404},
		{"deep path", http.MethodGet, flowTestOwnedTool + "/versions/" + flowTestOldVersion + "/files", 404},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given a malformed version route.
			fixture, service, record := flowToolFixtureService(t, flowToolDefaultRows())
			// When it is handled.
			status, _, _ := flowToolCall(t, service, record, false, scenario.method, scenario.tail, "")
			// Then no version RPC is issued.
			if status != scenario.status || fixture.count("ZLaYcd") != 0 || fixture.count("CvpHmb") != 0 {
				t.Fatalf("status=%d", status)
			}
		})
	}
}

func TestFlowToolVersionRestoreReadsBackCurrentVersion(t *testing.T) {
	// Given an owned tool whose provider restore moves the current version.
	fixture, service, record := flowToolFixtureService(t, flowToolDefaultRows())
	restored := flowToolRowFixture(flowTestOwnedTool, "Owned", 0, flowTestThirdVersion)
	fixture.reply("tRARke", flowToolListReply(t, [][]any{restored}))
	fixture.reply("CvpHmb", rpcEnvelope(t, "CvpHmb", []any{}))

	// When an older version is restored.
	status, code, body := flowToolCall(t, service, record, false, http.MethodPost, flowTestOwnedTool+"/versions/"+flowTestOldVersion+":restore", "")

	// Then one restore names tool and version, and the response carries the read-back tool.
	var result struct {
		RestoredVersionID string           `json:"restoredVersionId"`
		Tool              flowToolResource `json:"tool"`
	}
	args := fixture.args("CvpHmb", 0)
	if err := json.Unmarshal(body, &result); err != nil || status != 200 || result.RestoredVersionID != flowTestOldVersion ||
		result.Tool.VersionID != flowTestThirdVersion || fixture.count("CvpHmb") != 1 ||
		len(args) != 2 || args[0] != flowTestOwnedTool || args[1] != flowTestOldVersion {
		t.Fatalf("status=%d code=%s body=%s args=%v err=%v", status, code, body, args, err)
	}
}
