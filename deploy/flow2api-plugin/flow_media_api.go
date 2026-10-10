package main

import (
	"context"
	"net/http"
	"strings"
)

type flowMediaResource struct {
	ID         string `json:"id"`
	ProjectID  string `json:"projectId"`
	WorkflowID string `json:"workflowId,omitempty"`
	Title      string `json:"title,omitempty"`
	Type       string `json:"type"`
	MIMEType   string `json:"mimeType,omitempty"`
	URL        string `json:"url,omitempty"`
	Archived   bool   `json:"archived"`
}

func decodeFlowMedia(value any, project string) (flowMediaResource, error) {
	var result flowMediaResource
	result.ID, _ = jsonField(value, 0).(string)
	result.ProjectID, _ = jsonField(value, 1).(string)
	result.WorkflowID, _ = jsonField(value, 2).(string)
	if !flowIdentifier(result.ID) || result.ProjectID != project {
		return result, failure(502, "flow_media_identity_mismatch")
	}
	switch {
	case jsonField(value, 6) != nil:
		result.Type = "image"
		result.MIMEType, _ = jsonField(value, 6, 1, 6).(string)
	case jsonField(value, 7) != nil:
		result.Type, result.MIMEType = "video", "video/mp4"
	case jsonField(value, 10) != nil:
		result.Type = "audio"
	default:
		result.Type = "unknown"
	}
	result.URL = flowFindURL(value, result.Type)
	return result, nil
}

func (service *service) flowProjectContents(ctx context.Context, record storageRecord, project string) (any, error) {
	var payload any
	err := service.withFlowSession(ctx, record, func(session *flowSession) error {
		var err error
		payload, err = session.rpc(ctx, "Zzl0ze", []any{"projects/" + project, nil, nil, nil, []any{1}}, "/project/"+project, "")
		return err
	})
	return payload, err
}

func (service *service) getFlowMedia(ctx context.Context, record storageRecord, project, id string) (flowMediaResource, error) {
	var media flowMediaResource
	err := service.withFlowSession(ctx, record, func(session *flowSession) error {
		payload, err := session.rpc(ctx, "as29s", []any{id}, "/project/"+project, "")
		if err != nil {
			return err
		}
		media, err = decodeFlowMedia(payload, project)
		if err == nil && media.ID != id {
			return failure(502, "flow_media_identity_mismatch")
		}
		return err
	})
	return media, err
}

func (service *service) listFlowMedia(ctx context.Context, record storageRecord, project string, request flowHTTPRequest) (httpResponse, error) {
	contents, err := service.flowProjectContents(ctx, record, project)
	if err != nil {
		return httpResponse{}, err
	}
	workflows := map[string][]any{}
	rows, _ := jsonField(contents, 1).([]any)
	for _, row := range rows {
		workflow, ok := row.([]any)
		id, _ := jsonField(row, 0).(string)
		if ok && id != "" {
			workflows[id] = workflow
		}
	}
	archived := request.Query.Get("archived")
	if archived != "" && archived != "true" && archived != "false" {
		return httpResponse{}, failure(400, "flow_archived_filter_invalid")
	}
	kind := request.Query.Get("type")
	if kind != "" && kind != "image" && kind != "video" && kind != "audio" {
		return httpResponse{}, failure(400, "flow_media_type_invalid")
	}
	result := []flowMediaResource{}
	rows, _ = jsonField(contents, 2).([]any)
	for _, row := range rows {
		media, err := decodeFlowMedia(row, project)
		if err != nil {
			return httpResponse{}, err
		}
		workflow := workflows[media.WorkflowID]
		media.Title, _ = jsonField(workflow, 3, 0).(string)
		flag := jsonField(workflow, 3, 2)
		media.Archived = flag == true || flag == float64(1)
		if media.Archived != (archived == "true") || kind != "" && media.Type != kind {
			continue
		}
		if search := request.Query.Get("search"); search != "" && !strings.Contains(strings.ToLower(media.Title), strings.ToLower(search)) {
			continue
		}
		if collection := request.Query.Get("collectionId"); collection != "" && jsonField(workflow, 1) != collection {
			continue
		}
		result = append(result, media)
	}
	return flowJSON(http.StatusOK, map[string]any{"media": result})
}

func (service *service) archiveFlowMedia(ctx context.Context, record storageRecord, media flowMediaResource, archived bool) error {
	if !flowUUIDPattern.MatchString(media.WorkflowID) {
		return failure(409, "flow_media_workflow_unavailable")
	}
	return service.withFlowSession(ctx, record, func(session *flowSession) error {
		workflow := []any{media.WorkflowID, nil, nil, []any{nil, nil, archived}, media.ProjectID}
		_, err := session.rpc(ctx, "pGCYOe", []any{[]any{workflow}, []any{[]any{"metadata.archived"}}}, "/project/"+media.ProjectID, "")
		return err
	})
}

func (service *service) flowMediaHTTP(ctx context.Context, record storageRecord, project, tail string, request flowHTTPRequest) (httpResponse, error) {
	if tail == "" && request.Method == http.MethodGet {
		return service.listFlowMedia(ctx, record, project, request)
	}
	id, action, _ := strings.Cut(tail, ":")
	if !flowIdentifier(id) || strings.Contains(id, "/") {
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	media, err := service.getFlowMedia(ctx, record, project, id)
	if err != nil {
		return httpResponse{}, err
	}
	switch {
	case request.Method == http.MethodGet && action == "":
		return flowJSON(http.StatusOK, media)
	case request.Method == http.MethodGet && action == "download":
		if media.URL == "" {
			return httpResponse{}, failure(409, "flow_media_not_ready")
		}
		body, err := service.flowDownload(ctx, media.URL, 128<<20)
		if err != nil {
			return httpResponse{}, err
		}
		mimeType := media.MIMEType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		return httpResponse{StatusCode: 200, Headers: http.Header{
			"Content-Type": {mimeType}, "Cache-Control": {"private, no-store"}, "X-Content-Type-Options": {"nosniff"},
		}, Body: body}, nil
	case request.Method == http.MethodDelete && action == "", request.Method == http.MethodPost && action == "restore":
		if err := service.archiveFlowMedia(ctx, record, media, request.Method == http.MethodDelete); err != nil {
			return httpResponse{}, err
		}
		return httpResponse{StatusCode: 204, Headers: http.Header{"Cache-Control": {"private, no-store"}}}, nil
	default:
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
}
