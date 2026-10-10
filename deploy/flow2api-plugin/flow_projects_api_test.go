package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func flowHTTPTest(t *testing.T, service *service, request flowHTTPRequest) httpResponse {
	t.Helper()
	result := invoke(t, service, "frontend_http.handle", request)
	if !result.OK {
		t.Fatalf("native HTTP call failed: %+v", result.Error)
	}
	var response httpResponse
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestFlowProjectAPIUsesCapturedCRUDRequests(t *testing.T) {
	cases := []struct {
		method, path, body, rpc string
		payload                 any
		status                  int
	}{
		{"POST", "/v1/flow/projects", `{"title":"fixture"}`, "jHPbke", []any{flowTestProject, []any{"fixture"}}, 201},
		{"GET", "/v1/flow/projects/" + flowTestProject, "", "ngNC2", []any{flowTestProject, []any{"fixture"}}, 200},
		{"PATCH", "/v1/flow/projects/" + flowTestProject, `{"title":"fixture"}`, "o8DA4", []any{"fixture"}, 200},
		{"DELETE", "/v1/flow/projects/" + flowTestProject, "", "QI2zvc", []any{}, 204},
	}
	for _, scenario := range cases {
		t.Run(scenario.method, func(t *testing.T) {
			// Given an authenticated caller and the real RPC response shape.
			fixture := newFlowFixture(t)
			service, record := flowService(t, fixture)
			service.flowProjects = map[string]string{record.SourceAuthID: flowTestProject}
			fixture.reply(scenario.rpc, rpcEnvelope(t, scenario.rpc, scenario.payload))

			// When one project operation reaches the native frontend.
			response := flowHTTPTest(t, service, flowHTTPRequest{
				Method: scenario.method, Path: scenario.path, Body: []byte(scenario.body), CallerScope: strings.Repeat("a", 64),
			})

			// Then it performs exactly one correctly addressed upstream mutation.
			if response.StatusCode != scenario.status || fixture.count(scenario.rpc) != 1 {
				t.Fatalf("status=%d body=%s calls=%d", response.StatusCode, response.Body, fixture.count(scenario.rpc))
			}
			args := fixture.args(scenario.rpc, 0)
			switch scenario.method {
			case "POST":
				if jsonField(args, 0) != "projects/*" || jsonField(args, 1, 1, 0) != "fixture" {
					t.Fatalf("create args=%v", args)
				}
			case "GET":
				if jsonField(args, 0) != "tools/PINHOLE/projects/"+flowTestProject {
					t.Fatalf("get args=%v", args)
				}
			case "PATCH":
				if jsonField(args, 0) != "projects/"+flowTestProject || jsonField(args, 2, 0, 0) != "project_title" {
					t.Fatalf("rename args=%v", args)
				}
			case "DELETE":
				if jsonField(args, 0) != flowTestProject || service.flowProjects[record.SourceAuthID] != "" {
					t.Fatalf("delete args=%v cached=%v", args, service.flowProjects)
				}
			}
		})
	}
}

func TestFlowProjectListPreservesPagination(t *testing.T) {
	// Given a page with one existing account project and a continuation token.
	fixture := newFlowFixture(t)
	service, _ := flowService(t, fixture)
	fixture.reply("UpteDb", rpcEnvelope(t, "UpteDb", []any{
		[]any{[]any{flowTestProject, []any{"fixture"}}}, "next-token",
	}))

	// When the caller requests the next page.
	response := flowHTTPTest(t, service, flowHTTPRequest{
		Method: "GET", Path: "/v1/flow/projects", CallerScope: strings.Repeat("a", 64),
		Query: url.Values{"pageSize": {"12"}, "pageToken": {"previous-token"}},
	})

	// Then the native page token and named resources reach the client unchanged.
	var page flowProjectList
	if err := json.Unmarshal(response.Body, &page); err != nil {
		t.Fatal(err)
	}
	args := fixture.args("UpteDb", 0)
	if response.StatusCode != 200 || len(page.Projects) != 1 || page.Projects[0].ID != flowTestProject ||
		page.NextPageToken != "next-token" || jsonField(args, 1) != float64(12) || jsonField(args, 2) != "previous-token" {
		t.Fatalf("response=%s args=%v", response.Body, args)
	}
}

func TestFlowProjectAPIRejectsUntrustedRequestsBeforeUpstream(t *testing.T) {
	for _, scenario := range []struct {
		scope, body string
		status      int
	}{
		{"", `{"title":"fixture"}`, 401},
		{strings.Repeat("a", 64), `{"title":"fixture","cookie":"forged"}`, 400},
		{strings.Repeat("a", 64), `{"title":" "}`, 400},
		{strings.Repeat("a", 64), `{"title":"fixture"} {}`, 400},
	} {
		t.Run(scenario.scope+scenario.body, func(t *testing.T) {
			// Given invalid authentication or project input.
			fixture := newFlowFixture(t)
			service, _ := flowService(t, fixture)
			// When the malformed request enters the native frontend.
			response := flowHTTPTest(t, service, flowHTTPRequest{
				Method: http.MethodPost, Path: "/v1/flow/projects", CallerScope: scenario.scope, Body: []byte(scenario.body),
			})
			// Then no project is created.
			if response.StatusCode != scenario.status || fixture.count("jHPbke") != 0 {
				t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
			}
		})
	}
}

func TestFlowProjectCreationDoesNotReplayUnknownSubmission(t *testing.T) {
	// Given a connection lost after the create request reached the upstream.
	fixture := newFlowFixture(t)
	service, _ := flowService(t, fixture)
	fixture.dropRPC = "jHPbke"
	// When the client asks for one project.
	response := flowHTTPTest(t, service, flowHTTPRequest{
		Method: "POST", Path: "/v1/flow/projects", CallerScope: strings.Repeat("a", 64), Body: []byte(`{"title":"fixture"}`),
	})
	// Then the error is reported without duplicating the project.
	if response.StatusCode != 502 || fixture.count("jHPbke") != 1 {
		t.Fatalf("status=%d calls=%d", response.StatusCode, fixture.count("jHPbke"))
	}
}
