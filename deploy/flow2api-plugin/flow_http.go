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
	return json.RawMessage(`{"Routes":[{"Method":"GET","Path":"/v1/flow/projects"},{"Method":"POST","Path":"/v1/flow/projects"},{"Method":"GET","Path":"/v1/flow/projects/{project}"},{"Method":"PATCH","Path":"/v1/flow/projects/{project}"},{"Method":"DELETE","Path":"/v1/flow/projects/{project}"}]}`)
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
	id, matched := strings.CutPrefix(request.Path, "/v1/flow/projects/")
	if !matched || !flowUUIDPattern.MatchString(id) {
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
