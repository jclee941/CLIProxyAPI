package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"
)

func flowStreamTestReply(t *testing.T, events ...any) string {
	t.Helper()
	rows := []any{}
	for _, event := range events {
		rows = append(rows, []any{"wrb.fr", nil, string(jsonFixture(t, event))})
	}
	raw := jsonFixture(t, rows)
	return fmt.Sprintf(")]}'\n\n%d\n%s\n", len(utf16.Encode([]rune(string(raw)))), raw)
}

func flowAgentTestSession() []any {
	return []any{flowTestOp, []any{"fixture", []any{1700000000}, []any{1700000001}}}
}

func TestFlowAgentSessionCRUDUsesCapturedProtocol(t *testing.T) {
	for _, scenario := range []struct {
		method, tail, body, rpc string
		payload                 any
		status                  int
	}{
		{"POST", "", `{}`, "csbIsb", []any{flowAgentTestSession()}, 201},
		{"PATCH", "/" + flowTestOp, `{"title":"renamed"}`, "TunYMc", []any{}, 200},
		{"DELETE", "/" + flowTestOp, "", "Dcn2Le", []any{}, 204},
	} {
		t.Run(scenario.method, func(t *testing.T) {
			// Given a session in the selected project.
			fixture := newFlowFixture(t)
			service, _ := flowService(t, fixture)
			fixture.reply("mrlkwd", rpcEnvelope(t, "mrlkwd", []any{[]any{flowAgentTestSession()}}))
			fixture.reply(scenario.rpc, rpcEnvelope(t, scenario.rpc, scenario.payload))
			// When a caller manages that session.
			response := flowHTTPTest(t, service, flowHTTPRequest{Method: scenario.method,
				Path:        "/v1/flow/projects/" + flowTestProject + "/sessions" + scenario.tail,
				CallerScope: strings.Repeat("a", 64), Body: []byte(scenario.body)})
			// Then exactly one mutation reaches the correct RPC.
			if response.StatusCode != scenario.status || fixture.count(scenario.rpc) != 1 {
				t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
			}
			args := fixture.args(scenario.rpc, 0)
			if scenario.method == "POST" {
				seed, _ := jsonField(args, 2).(string)
				if jsonField(args, 0) != flowTestProject || !flowUUIDPattern.MatchString(seed) {
					t.Fatalf("create args=%v", args)
				}
			} else if jsonField(args, 0) != flowTestOp {
				t.Fatalf("session args=%v", args)
			}
		})
	}
}

func TestFlowAgentChatKeepsFinalMessagesAndStructuredValues(t *testing.T) {
	// Given repeated partial snapshots and a final structured reply.
	fixture := newFlowFixture(t)
	service, _ := flowService(t, fixture)
	fixture.reply("mrlkwd", rpcEnvelope(t, "mrlkwd", []any{[]any{flowAgentTestSession()}}))
	content := []any{[]any{[]any{"text", []any{nil, nil, "reply"}}, []any{"ready", []any{nil, nil, nil, 1}}}}
	message := []any{content, nil, "message-id"}
	fixture.streams = map[string]string{flowCreationStreamPath: flowStreamTestReply(t,
		[]any{message, nil, true}, []any{message}, []any{message})}
	// When the caller sends one turn.
	response := flowHTTPTest(t, service, flowHTTPRequest{Method: "POST",
		Path:        "/v1/flow/projects/" + flowTestProject + "/sessions/" + flowTestOp + ":chat",
		CallerScope: strings.Repeat("a", 64), Body: []byte(`{"prompt":"fixture"}`)})
	// Then completed snapshots are deduplicated and values retain their types.
	var result flowAgentChatResult
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || len(result.Messages) != 1 || result.Messages[0].Content["ready"] != true ||
		result.SessionID != flowTestOp || fixture.count("request:agent_"+flowTestOp) != 1 {
		t.Fatalf("response=%s", response.Body)
	}
	form := fixture.calls[flowCreationStreamPath][0]
	if form.Get("deadline") != "" {
		t.Fatal("post-connection deadline was sent")
	}
	var outer []any
	if err := json.Unmarshal([]byte(form.Get("f.req")), &outer); err != nil {
		t.Fatal(err)
	}
	var inner []any
	if err := json.Unmarshal([]byte(outer[1].(string)), &inner); err != nil {
		t.Fatal(err)
	}
	if inner[0] != flowTestOp || jsonField(inner, 2, 0) != "projects/"+flowTestProject {
		t.Fatalf("stream arguments=%v", inner)
	}
}

func TestFlowToolBuilderReturnsRealFilesAndVersion(t *testing.T) {
	// Given a successful builder stream with a full source file.
	fixture := newFlowFixture(t)
	service, _ := flowService(t, fixture)
	fixture.reply("ngNC2", rpcEnvelope(t, "ngNC2", []any{flowTestProject, []any{"fixture"}}))
	fixture.streams = map[string]string{flowAppletStreamPath: flowStreamTestReply(t,
		[]any{flowTestMedia, "done"},
		[]any{flowTestMedia, nil, nil, nil, []any{[]any{"App.tsx", "text/typescript-jsx", "export default 1;"}}, nil, flowTestOp})}
	// When a caller creates a tool.
	response := flowHTTPTest(t, service, flowHTTPRequest{Method: "POST", Path: "/v1/flow/tools",
		CallerScope: strings.Repeat("a", 64), Body: jsonFixture(t, map[string]any{"projectId": flowTestProject, "prompt": "fixture", "requestId": "fixture-build"})})
	// Then actual provider identifiers and source bytes are returned.
	var result flowToolRun
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 201 || result.ToolID != flowTestMedia || result.VersionID != flowTestOp ||
		len(result.Files) != 1 || result.Files[0].Content != "export default 1;" || fixture.count("request:fixture-build") != 1 {
		t.Fatalf("response=%s", response.Body)
	}
}

func TestFlowToolBuilderDoesNotRepeatAnUnknownSubmission(t *testing.T) {
	// Given a response lost after the stream POST was accepted.
	fixture := newFlowFixture(t)
	service, _ := flowService(t, fixture)
	fixture.reply("ngNC2", rpcEnvelope(t, "ngNC2", []any{flowTestProject, []any{"fixture"}}))
	fixture.dropStream = true
	// When a caller creates one tool.
	response := flowHTTPTest(t, service, flowHTTPRequest{Method: "POST", Path: "/v1/flow/tools",
		CallerScope: strings.Repeat("a", 64), Body: jsonFixture(t, map[string]any{"projectId": flowTestProject, "prompt": "fixture"})})
	// Then uncertainty is explicit and the mutation is not repeated.
	if response.StatusCode != 502 || fixture.count(flowAppletStreamPath) != 1 ||
		!strings.Contains(string(response.Body), `"flow_submission_outcome_unknown"`) {
		t.Fatalf("response=%s posts=%d", response.Body, fixture.count(flowAppletStreamPath))
	}
}
