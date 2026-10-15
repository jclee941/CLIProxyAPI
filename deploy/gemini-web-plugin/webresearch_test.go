package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

type researchWebFixture struct {
	mu sync.Mutex
	// turns are the generation field arrays the plugin sent, in order.
	turns [][]any
	// ready fills the report's body, which stays empty while the agent works.
	ready bool
	// question makes the agent answer the opening turn with a question back.
	question bool
	// unreadable answers the opening turn with neither text nor a plan.
	unreadable bool
	reads      int
}

func researchWeb(t *testing.T, service *service, fixture *researchWebFixture) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		switch {
		case strings.HasSuffix(request.URL.Path, "/app"):
			writeIdentityFixture(t, writer)
		case strings.HasSuffix(request.URL.Path, "/StreamGenerate"):
			if err := request.ParseForm(); err != nil {
				t.Error(err)
				return
			}
			var envelope []any
			var fields []any
			if json.Unmarshal([]byte(request.Form.Get("f.req")), &envelope) != nil || json.Unmarshal([]byte(jsonField(envelope, 1).(string)), &fields) != nil {
				t.Error("unreadable generation request")
				return
			}
			fixture.turns = append(fixture.turns, fields)
			n := len(fixture.turns)
			candidate := slots(31, map[int]any{0: fmt.Sprintf("rc_%d", n)})
			prompt, _ := jsonField(fields, 0, 0).(string)
			switch research := jsonField(fields, 49) == float64(1); {
			case research && prompt == webResearchStart:
				candidate[1] = []any{"I'm on it. The report will be ready in a few minutes."}
				candidate[30] = []any{researchDocument("")}
			case research && fixture.unreadable && n == 1:
				candidate[12] = slots(71, map[int]any{70: []any{"card"}})
			case research && fixture.question && n == 1:
				candidate[1] = []any{"Which region should the research cover?"}
			case research:
				candidate[1] = []any{"Here's the plan. Edit it or start the research."}
				candidate[12] = slots(56, map[int]any{55: []any{"Battery recycling", []any{[]any{nil, "Market", "Size the market"}, []any{nil, "Policy", "Compare the rules"}}, "Ready in a few mins", []any{webResearchStart}}})
			default:
				candidate[1] = []any{"The second section covers costs."}
			}
			frame := slots(26, map[int]any{1: []any{"c_research", fmt.Sprintf("r_%d", n)}, 4: []any{candidate}, 25: fmt.Sprintf("context_%d", n)})
			writeFixture(t, writer, string(jsonFixture(t, []any{[]any{"wrb.fr", nil, string(jsonFixture(t, frame))}}))+"\n")
		case strings.HasSuffix(request.URL.Path, "/batchexecute"):
			switch request.URL.Query().Get("rpcids") {
			case accountCapabilityRPC:
				writeFixture(t, writer, rpcEnvelope(t, accountCapabilityRPC, slots(17, map[int]any{14: 1000, 15: []any{slots(18, map[int]any{0: "cap-flash", 11: "3.8 Flash", 17: 1})}})))
			case webVideoTurnsRPC:
				fixture.reads++
				body := ""
				if fixture.ready {
					body = "Recycling capacity grew threefold [cite: 1]."
				}
				start := slots(4, map[int]any{0: []any{nil, "r_2"}, 3: []any{[]any{slots(31, map[int]any{0: "rc_2", 30: []any{researchDocument(body)}})}}})
				plan := slots(4, map[int]any{0: []any{nil, "r_1"}, 3: []any{[]any{slots(13, map[int]any{0: "rc_1"})}}})
				writeFixture(t, writer, rpcEnvelope(t, webVideoTurnsRPC, []any{[]any{start, plan}}))
			default:
				t.Errorf("unexpected RPC %s", request.URL.Query().Get("rpcids"))
			}
		case request.URL.Path == "/RotateCookies":
			writer.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected path: %s", request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	service.client = server.Client()
	service.client.Transport = continuationFixtureTransport{base: service.client.Transport, origin: server.URL}
	service.webOriginOverride, service.webUploadOverride, service.webRotateOverride = server.URL, server.URL, server.URL
	t.Cleanup(server.Close)
}

// researchDocument is the report a research turn attaches: a task id names it
// research, and its body stays empty until the research ends.
func researchDocument(body string) []any {
	source := []any{nil, nil, nil, []any{[]any{"favicon", "https://example.org/recycling", "Recycling report"}}}
	return slots(6, map[int]any{0: "doc_1", 2: "Battery recycling", 3: "task_1", 4: body, 5: slots(44, map[int]any{43: []any{[]any{[]any{" [cite: 1]"}, []any{source}}}})})
}

func researchCall(t *testing.T, service *service, local localSession, body string) envelope {
	t.Helper()
	auth, err := authFromRecord(local.Target)
	if err != nil {
		t.Fatal(err)
	}
	request := executorRequest{AuthID: local.Target.ID, AuthProvider: provider, Model: webResearchModel, Format: "interactions", SourceFormat: "interactions", StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, Payload: []byte(body)}
	request.Metadata.CallerScope = testCallerScope
	return invoke(t, service, "executor.execute", request)
}

// researchGet is the poll the host turns GET /v1beta/interactions/{id} into.
func researchGet(t *testing.T, service *service, local localSession, id string) envelope {
	t.Helper()
	auth, err := authFromRecord(local.Target)
	if err != nil {
		t.Fatal(err)
	}
	request := executorRequest{AuthID: local.Target.ID, AuthProvider: provider, Model: interactionOmniModel, Format: "interactions", SourceFormat: "interactions", Alt: interactionRetrieveAlt, StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, Payload: []byte(`{"model":"gemini-omni-1.1-flash","id":"` + id + `"}`)}
	request.Metadata.CallerScope = testCallerScope
	return invoke(t, service, "executor.execute", request)
}

type researchView struct {
	ID, Status, Text string
}

func researchResponse(t *testing.T, result envelope) researchView {
	t.Helper()
	if !result.OK {
		t.Fatalf("research failed: %+v", result.Error)
	}
	var response struct{ Payload []byte }
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	var body struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Status string `json:"status"`
		Steps  []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(response.Payload, &body); err != nil || body.Model != webResearchModel {
		t.Fatalf("response %s: %v", response.Payload, err)
	}
	view := researchView{ID: body.ID, Status: body.Status}
	for _, step := range body.Steps {
		for _, content := range step.Content {
			if content.Type == "text" {
				view.Text += content.Text
			}
		}
	}
	return view
}

func (fixture *researchWebFixture) sent() [][]any {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return slices.Clone(fixture.turns)
}

func TestResearchStartsItsPlanAndAnswersWithTheReport(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &researchWebFixture{}
	researchWeb(t, service, fixture)

	created := researchResponse(t, researchCall(t, service, local, `{"model":"gemini-deep-research","input":"How is battery recycling changing?","background":true,"agent_config":{"type":"deep-research","thinking_summaries":"auto"}}`))

	// The plan is started at once, in its own conversation, as the web app's
	// Start research button does; both turns go to the research tool.
	turns := fixture.sent()
	if created.Status != "in_progress" || len(turns) != 2 || jsonField(turns[1], 0, 0) != webResearchStart || jsonField(turns[1], 2, 0) != "c_research" || jsonField(turns[1], 2, 1) != "r_1" {
		t.Fatalf("created %+v after turns %v", created, turns)
	}
	for _, fields := range turns {
		token, _ := jsonField(fields, 3).(string)
		if jsonField(fields, 49) != float64(1) || jsonField(fields, 55, 0, 0) != float64(1) || !strings.HasPrefix(token, "!") || jsonField(fields, 4) == nil {
			t.Fatalf("turn not sent to the research tool: 3=%.8q 4=%v 49=%v 54=%v 55=%v", token, jsonField(fields, 4), jsonField(fields, 49), jsonField(fields, 54), jsonField(fields, 55))
		}
	}
	if pending := researchResponse(t, researchGet(t, service, local, created.ID)); pending.Status != "in_progress" || pending.Text != "" {
		t.Fatalf("poll while researching = %+v", pending)
	}
	fixture.mu.Lock()
	fixture.ready = true
	fixture.mu.Unlock()
	done := researchResponse(t, researchGet(t, service, local, created.ID))
	if done.Status != "completed" || done.ID != created.ID || !strings.HasPrefix(done.Text, "# Battery recycling\n\nRecycling capacity grew threefold [cite: 1].") || !strings.HasSuffix(done.Text, "## Sources\n\n1. [Recycling report](https://example.org/recycling)") {
		t.Fatalf("finished research = %+v", done)
	}
	// The report is kept: a later poll answers without reading the chat again.
	fixture.mu.Lock()
	reads := fixture.reads
	fixture.mu.Unlock()
	if again := researchResponse(t, researchGet(t, service, local, created.ID)); again != done {
		t.Fatalf("stored report = %+v", again)
	}
	fixture.mu.Lock()
	reread := fixture.reads != reads
	fixture.mu.Unlock()
	if reread {
		t.Fatal("a finished research read the chat again")
	}

	// A question about the report continues its conversation as a plain turn.
	follow := researchResponse(t, researchCall(t, service, local, `{"model":"gemini-deep-research","input":"What does the second section say?","background":true,"previous_interaction_id":"`+created.ID+`"}`))
	turns = fixture.sent()
	last := turns[len(turns)-1]
	if follow.Status != "completed" || follow.Text != "The second section covers costs." || jsonField(last, 49) == float64(1) || jsonField(last, 2, 1) != "r_2" {
		t.Fatalf("follow-up %+v sent %v", follow, last)
	}
}

func TestResearchReturnsThePlanForReviewAndStartsItOnApproval(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &researchWebFixture{}
	researchWeb(t, service, fixture)
	collaborative := `"agent_config":{"type":"deep-research","collaborative_planning":true}`

	planned := researchResponse(t, researchCall(t, service, local, `{"model":"gemini-deep-research","input":"How is battery recycling changing?","background":true,`+collaborative+`}`))
	if planned.Status != "completed" || !strings.Contains(planned.Text, "## Battery recycling\n\n1. Market: Size the market\n2. Policy: Compare the rules\n\nReady in a few mins") || len(fixture.sent()) != 1 {
		t.Fatalf("plan for review = %+v", planned)
	}
	// A reply that keeps planning asks for a revised plan in the conversation.
	revised := researchResponse(t, researchCall(t, service, local, `{"model":"gemini-deep-research","input":"Focus on Europe","background":true,"previous_interaction_id":"`+planned.ID+`",`+collaborative+`}`))
	turns := fixture.sent()
	if revised.Status != "completed" || len(turns) != 2 || jsonField(turns[1], 0, 0) != "Focus on Europe" || jsonField(turns[1], 2, 1) != "r_1" {
		t.Fatalf("revised %+v after %v", revised, turns)
	}
	// Approval starts the revised plan with the plan's own action, not the
	// words it came with.
	started := researchResponse(t, researchCall(t, service, local, `{"model":"gemini-deep-research","input":"Looks good","background":true,"previous_interaction_id":"`+revised.ID+`"}`))
	turns = fixture.sent()
	if started.Status != "in_progress" || len(turns) != 3 || jsonField(turns[2], 0, 0) != webResearchStart || jsonField(turns[2], 2, 1) != "r_2" {
		t.Fatalf("approved %+v after %v", started, turns)
	}
	if again := researchCall(t, service, local, `{"model":"gemini-deep-research","input":"more","background":true,"previous_interaction_id":"`+started.ID+`"}`); again.OK || !strings.Contains(again.Error.Message, "research_in_progress") {
		t.Fatalf("a running research was continued: %+v", again)
	}
}

func TestResearchQuestionBackIsAnsweredAndThePlanStarted(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &researchWebFixture{question: true}
	researchWeb(t, service, fixture)

	asked := researchResponse(t, researchCall(t, service, local, `{"model":"gemini-deep-research","input":"Research batteries","background":true}`))
	if asked.Status != "completed" || asked.Text != "Which region should the research cover?" {
		t.Fatalf("question back = %+v", asked)
	}
	started := researchResponse(t, researchCall(t, service, local, `{"model":"gemini-deep-research","input":"Europe","background":true,"previous_interaction_id":"`+asked.ID+`"}`))
	turns := fixture.sent()
	if started.Status != "in_progress" || len(turns) != 3 || jsonField(turns[1], 0, 0) != "Europe" || jsonField(turns[2], 0, 0) != webResearchStart {
		t.Fatalf("answered %+v after %v", started, turns)
	}
}

// An answer this parser cannot read names where its content went, without the
// content, so the next wire change can be located from one failure.
func TestResearchUnreadableAnswerReportsItsLayout(t *testing.T) {
	service, local := continuationFixture(t)
	researchWeb(t, service, &researchWebFixture{unreadable: true})

	result := researchCall(t, service, local, `{"model":"gemini-deep-research","input":"How is battery recycling changing?","background":true}`)

	if result.OK || result.Error == nil || !strings.Contains(result.Error.Message, "research_reply_unreadable") || !strings.Contains(result.Error.Message, "r=70") || strings.Contains(result.Error.Message, "card") {
		t.Fatalf("unreadable answer = %+v", result.Error)
	}
}

func TestResearchPollLeavesABusyAccountAlone(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &researchWebFixture{ready: true}
	researchWeb(t, service, fixture)
	created := researchResponse(t, researchCall(t, service, local, `{"model":"gemini-deep-research","input":"How is battery recycling changing?","background":true}`))
	video, err := service.acquireCredential(local.Target.TokenRef, true)
	if err != nil {
		t.Fatal(err)
	}

	polled := researchResponse(t, researchGet(t, service, local, created.ID))
	video.guard.Unlock()

	fixture.mu.Lock()
	reads := fixture.reads
	fixture.mu.Unlock()
	if polled.Status != "in_progress" || reads != 0 {
		t.Fatalf("poll beside a video = %+v after %d reads", polled, reads)
	}
	if done := researchResponse(t, researchGet(t, service, local, created.ID)); done.Status != "completed" {
		t.Fatalf("poll once the account was free = %+v", done)
	}
}

func TestResearchHoldsRequestsToTheAgentContract(t *testing.T) {
	service, local := continuationFixture(t)
	for _, body := range []string{
		`{"model":"gemini-deep-research","input":"x"}`,
		`{"model":"gemini-deep-research","input":"x","background":true,"store":false}`,
		`{"model":"gemini-deep-research","input":"x","background":true,"stream":true}`,
		`{"model":"gemini-deep-research","input":"x","background":true,"agent_config":{"type":"dynamic"}}`,
		`{"model":"gemini-deep-research","input":"x","background":true,"agent_config":{"type":"deep-research","visualization":"always"}}`,
		`{"model":"gemini-deep-research","input":"x","background":true,"tools":[{"type":"mcp_server"}]}`,
		`{"model":"gemini-deep-research","input":"","background":true}`,
	} {
		t.Run(body, func(t *testing.T) {
			if result := researchCall(t, service, local, body); result.OK {
				t.Fatal("request outside the agent contract was accepted")
			}
		})
	}
}
