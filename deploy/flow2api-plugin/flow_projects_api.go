package main

import (
	"context"
	"net/http"
)

type flowProjectResource struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type flowProjectList struct {
	Projects      []flowProjectResource `json:"projects"`
	NextPageToken string                `json:"nextPageToken,omitempty"`
}

func decodeFlowProject(value any) (flowProjectResource, error) {
	id, ok := jsonField(value, 0).(string)
	if !ok || !flowUUIDPattern.MatchString(id) {
		return flowProjectResource{}, failure(502, "flow_project_response_invalid")
	}
	title, _ := jsonField(value, 1, 0).(string)
	return flowProjectResource{ID: id, Title: title}, nil
}

func (service *service) listFlowProjects(ctx context.Context, record storageRecord, pageSize int, pageToken string) (flowProjectList, error) {
	result := flowProjectList{Projects: []flowProjectResource{}}
	err := service.withFlowSession(ctx, record, func(session *flowSession) error {
		payload, err := session.rpc(ctx, "UpteDb", []any{"projects/*", pageSize, pageToken, nil, nil, nil, []any{1}}, flowProjectsPath, "")
		if err != nil {
			return err
		}
		if _, valid := payload.([]any); !valid {
			return failure(502, "flow_project_list_invalid")
		}
		rows, ok := jsonField(payload, 0).([]any)
		if !ok && jsonField(payload, 0) != nil {
			return failure(502, "flow_project_list_invalid")
		}
		for _, row := range rows {
			project, err := decodeFlowProject(row)
			if err != nil {
				return err
			}
			result.Projects = append(result.Projects, project)
		}
		result.NextPageToken, _ = jsonField(payload, 1).(string)
		return nil
	})
	return result, err
}

func (service *service) createFlowProject(ctx context.Context, record storageRecord, title string) (flowProjectResource, error) {
	var result flowProjectResource
	err := service.withFlowSession(ctx, record, func(session *flowSession) error {
		payload, err := session.rpc(ctx, "jHPbke", []any{"projects/*", []any{nil, []any{title}}, []any{nil, 22}}, flowProjectsPath, "")
		if err != nil {
			return err
		}
		result, err = decodeFlowProject(payload)
		return err
	})
	return result, err
}

func (service *service) getFlowProject(ctx context.Context, record storageRecord, id string) (flowProjectResource, error) {
	var result flowProjectResource
	err := service.withFlowSession(ctx, record, func(session *flowSession) error {
		payload, err := session.rpc(ctx, "ngNC2", []any{"tools/PINHOLE/projects/" + id}, "/project/"+id, "")
		if err != nil {
			return err
		}
		result, err = decodeFlowProject(payload)
		if err == nil && result.ID != id {
			return failure(http.StatusBadGateway, "flow_project_identity_mismatch")
		}
		return err
	})
	return result, err
}

func (service *service) renameFlowProject(ctx context.Context, record storageRecord, id, title string) (flowProjectResource, error) {
	err := service.withFlowSession(ctx, record, func(session *flowSession) error {
		_, err := session.rpc(ctx, "o8DA4", []any{"projects/" + id, []any{title}, []any{[]any{"project_title"}}, []any{nil, 22}}, "/project/"+id, "")
		return err
	})
	return flowProjectResource{ID: id, Title: title}, err
}

func (service *service) deleteFlowProject(ctx context.Context, record storageRecord, id string) error {
	err := service.withFlowSession(ctx, record, func(session *flowSession) error {
		_, err := session.rpc(ctx, "QI2zvc", []any{id}, flowProjectsPath, "")
		return err
	})
	if err != nil {
		return err
	}
	service.flowMu.Lock()
	if service.flowProjects[record.SourceAuthID] == id {
		delete(service.flowProjects, record.SourceAuthID)
	}
	service.flowMu.Unlock()
	return nil
}
