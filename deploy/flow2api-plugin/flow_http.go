package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type flowHTTPRequest struct {
	Method      string
	Path        string
	Query       url.Values
	Headers     http.Header
	Body        []byte
	CallerScope string `json:"caller_scope"`
}

var flowCallerScopePattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func flowHTTPRegistration() json.RawMessage {
	return json.RawMessage(`{"Routes":[
{"Method":"GET","Path":"/v1/flow/projects"},
{"Method":"POST","Path":"/v1/flow/projects"},
{"Method":"GET","Path":"/v1/flow/projects/{project}"},
{"Method":"PATCH","Path":"/v1/flow/projects/{project}"},
{"Method":"DELETE","Path":"/v1/flow/projects/{project}"},
{"Method":"GET","Path":"/v1/flow/projects/{project}/media"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/media"},
{"Method":"GET","Path":"/v1/flow/projects/{project}/media/{media}"},
{"Method":"GET","Path":"/v1/flow/projects/{project}/media/{media}:download"},
{"Method":"DELETE","Path":"/v1/flow/projects/{project}/media/{media}"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/media/{media}:restore"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/uploads"},
{"Method":"GET","Path":"/v1/flow/uploads/{upload}"},
{"Method":"POST","Path":"/v1/flow/uploads/{upload}"},
{"Method":"DELETE","Path":"/v1/flow/uploads/{upload}"},
{"Method":"GET","Path":"/v1/flow/projects/{project}/collections"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/collections"},
{"Method":"GET","Path":"/v1/flow/projects/{project}/collections/{collection}"},
{"Method":"PATCH","Path":"/v1/flow/projects/{project}/collections/{collection}"},
{"Method":"DELETE","Path":"/v1/flow/projects/{project}/collections/{collection}"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/collections/{collection}:restore"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/collections/{collection}:purge"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/collections/{collection}:addItems"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/collections/{collection}:removeItems"},
{"Method":"GET","Path":"/v1/flow/projects/{project}/scenes"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/scenes"},
{"Method":"GET","Path":"/v1/flow/projects/{project}/scenes/{scene}"},
{"Method":"PATCH","Path":"/v1/flow/projects/{project}/scenes/{scene}"},
{"Method":"DELETE","Path":"/v1/flow/projects/{project}/scenes/{scene}"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/scenes/{scene}:restore"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/scenes/{scene}:purge"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/scenes/{scene}:copy"},
{"Method":"GET","Path":"/v1/flow/projects/{project}/scenes/{scene}/clips"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/scenes/{scene}/clips"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/scenes/{scene}/clips:reorder"},
{"Method":"PATCH","Path":"/v1/flow/projects/{project}/scenes/{scene}/clips/{position}"},
{"Method":"DELETE","Path":"/v1/flow/projects/{project}/scenes/{scene}/clips/{position}"},
{"Method":"GET","Path":"/v1/flow/projects/{project}/workflows"},
{"Method":"GET","Path":"/v1/flow/projects/{project}/workflows/{workflow}"},
{"Method":"PATCH","Path":"/v1/flow/projects/{project}/workflows/{workflow}"},
{"Method":"DELETE","Path":"/v1/flow/projects/{project}/workflows/{workflow}"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/workflows/{workflow}:restore"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/workflows/{workflow}:purge"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/workflows/{workflow}:copy"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/workflows/{workflow}:trim"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/workflows:batchArchive"},
{"Method":"GET","Path":"/v1/flow/projects/{project}/voices"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/voices:preview"},
{"Method":"GET","Path":"/v1/flow/projects/{project}/entities"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/entities"},
{"Method":"GET","Path":"/v1/flow/projects/{project}/entities/{entity}"},
{"Method":"PATCH","Path":"/v1/flow/projects/{project}/entities/{entity}"},
{"Method":"DELETE","Path":"/v1/flow/projects/{project}/entities/{entity}"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/entities/{entity}:restore"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/entities/{entity}:purge"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/entities/{entity}:copy"},
{"Method":"POST","Path":"/v1/flow/projects/{project}/entities/{entity}/images"},
{"Method":"DELETE","Path":"/v1/flow/projects/{project}/entities/{entity}/images/{slot}"},
{"Method":"GET","Path":"/v1/flow/likenesses"},
{"Method":"GET","Path":"/v1/flow/likenesses:eligibility"},
{"Method":"POST","Path":"/v1/flow/likenesses/registrations"},
{"Method":"GET","Path":"/v1/flow/likenesses/registrations/{token}"},
{"Method":"GET","Path":"/v1/flow/likenesses/{likeness}"},
{"Method":"DELETE","Path":"/v1/flow/likenesses/{likeness}"}
]}`)
}

func flowJSON(status int, value any) (httpResponse, error) {
	body, err := json.Marshal(value)
	return httpResponse{StatusCode: status, Headers: http.Header{
		"Content-Type": {"application/json"}, "Cache-Control": {"private, no-store"},
	}, Body: body}, err
}

func (service *service) flowHTTP(ctx context.Context, raw []byte) (httpResponse, error) {
	var request flowHTTPRequest
	var response httpResponse
	err := json.Unmarshal(raw, &request)
	if err != nil {
		err = failure(400, "flow_request_invalid")
	} else {
		response, err = service.flowHTTPRoute(ctx, request)
	}
	if err == nil {
		return response, nil
	}
	public := failure(500, "flow_operation_failed")
	var known *publicError
	if errors.As(err, &known) {
		public = known
	}
	return flowJSON(public.HTTPStatus, map[string]any{"error": map[string]any{
		"code": public.Code, "message": public.Message,
	}})
}

func flowProjectTitle(body []byte) (string, error) {
	var input struct {
		Title string `json:"title"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		return "", failure(400, "flow_project_request_invalid")
	}
	title := strings.TrimSpace(input.Title)
	if !utf8.ValidString(title) || title == "" || utf8.RuneCountInString(title) > 256 || strings.ContainsFunc(title, unicode.IsControl) {
		return "", failure(400, "flow_project_title_invalid")
	}
	return title, nil
}

func (service *service) flowHTTPRoute(ctx context.Context, request flowHTTPRequest) (httpResponse, error) {
	if !flowCallerScopePattern.MatchString(request.CallerScope) {
		return httpResponse{}, failure(401, "flow_authenticated_caller_required")
	}
	accounts := service.settings().FlowAccounts
	if len(accounts) != 1 {
		return httpResponse{}, failure(409, "flow_single_account_required")
	}
	record := storageRecord{SourceAuthID: accounts[0]}
	if request.Path == "/v1/flow/likenesses:eligibility" {
		return service.flowLikenessHTTP(ctx, record, "eligibility", request)
	}
	if request.Path == "/v1/flow/likenesses" {
		return service.flowLikenessHTTP(ctx, record, "", request)
	}
	if tail, matched := strings.CutPrefix(request.Path, "/v1/flow/likenesses/"); matched {
		return service.flowLikenessHTTP(ctx, record, tail, request)
	}
	if token, matched := strings.CutPrefix(request.Path, "/v1/flow/uploads/"); matched {
		return service.flowVideoUploadHTTP(ctx, record, token, request)
	}
	if request.Path == "/v1/flow/projects" {
		switch request.Method {
		case http.MethodGet:
			pageSize := 20
			if value := request.Query.Get("pageSize"); value != "" {
				parsed, err := strconv.Atoi(value)
				if err != nil || parsed < 1 || parsed > 100 {
					return httpResponse{}, failure(400, "flow_page_size_invalid")
				}
				pageSize = parsed
			}
			pageToken := request.Query.Get("pageToken")
			if len(pageToken) > 8192 {
				return httpResponse{}, failure(400, "flow_page_token_invalid")
			}
			result, err := service.listFlowProjects(ctx, record, pageSize, pageToken)
			if err != nil {
				return httpResponse{}, err
			}
			return flowJSON(http.StatusOK, result)
		case http.MethodPost:
			title, err := flowProjectTitle(request.Body)
			if err != nil {
				return httpResponse{}, err
			}
			result, err := service.createFlowProject(ctx, record, title)
			if err != nil {
				return httpResponse{}, err
			}
			return flowJSON(http.StatusCreated, result)
		}
	}
	projectPath, matched := strings.CutPrefix(request.Path, "/v1/flow/projects/")
	id, subpath, nested := strings.Cut(projectPath, "/")
	if !matched || !flowUUIDPattern.MatchString(id) {
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	if nested {
		if subpath == "uploads" && request.Method == http.MethodPost {
			return service.startFlowVideoUpload(ctx, record, id, request.Body)
		}
		resource, tail, _ := strings.Cut(subpath, "/")
		switch resource {
		case "collections":
			return service.flowCollectionHTTP(ctx, record, id, tail, request)
		case "scenes":
			return service.flowSceneHTTP(ctx, record, id, tail, request)
		case "workflows":
			return service.flowWorkflowHTTP(ctx, record, id, tail, request)
		case "workflows:batchArchive":
			return service.flowWorkflowHTTP(ctx, record, id, ":batchArchive", request)
		case "voices":
			return service.flowVoiceHTTP(ctx, record, id, tail, request)
		case "voices:preview":
			return service.flowVoiceHTTP(ctx, record, id, ":preview", request)
		case "entities":
			return service.flowEntityHTTP(ctx, record, id, tail, request)
		}
		if subpath == "media" {
			return service.flowMediaHTTP(ctx, record, id, "", request)
		}
		if tail, ok := strings.CutPrefix(subpath, "media/"); ok {
			return service.flowMediaHTTP(ctx, record, id, tail, request)
		}
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	switch request.Method {
	case http.MethodGet:
		result, err := service.getFlowProject(ctx, record, id)
		if err != nil {
			return httpResponse{}, err
		}
		return flowJSON(http.StatusOK, result)
	case http.MethodPatch:
		title, err := flowProjectTitle(request.Body)
		if err != nil {
			return httpResponse{}, err
		}
		result, err := service.renameFlowProject(ctx, record, id, title)
		if err != nil {
			return httpResponse{}, err
		}
		return flowJSON(http.StatusOK, result)
	case http.MethodDelete:
		if err := service.deleteFlowProject(ctx, record, id); err != nil {
			return httpResponse{}, err
		}
		return httpResponse{StatusCode: http.StatusNoContent, Headers: http.Header{"Cache-Control": {"private, no-store"}}}, nil
	default:
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
}
