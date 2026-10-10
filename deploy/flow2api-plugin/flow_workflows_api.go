package main

import (
	"context"
	"math"
	"net/http"
	"slices"
)

type flowWorkflowResource struct {
	ID             string   `json:"id"`
	ProjectID      string   `json:"projectId"`
	CollectionID   string   `json:"collectionId,omitempty"`
	Title          string   `json:"title"`
	CreatedAt      string   `json:"createdAt,omitempty"`
	Archived       bool     `json:"archived"`
	Favorited      bool     `json:"favorited"`
	PrimaryMediaID string   `json:"primaryMediaId,omitempty"`
	MediaIDs       []string `json:"mediaIds"`
}

type flowWorkflowList struct {
	Workflows []flowWorkflowResource `json:"workflows"`
}

type flowWorkflowUpdate struct {
	Title          *string `json:"title"`
	Favorited      *bool   `json:"favorited"`
	Archived       *bool   `json:"archived"`
	CollectionID   *string `json:"collectionId"`
	PrimaryMediaID *string `json:"primaryMediaId"`
}

type flowWorkflowCopy struct {
	SourceProjectID string `json:"sourceProjectId"`
	CollectionID    string `json:"collectionId"`
}

type flowWorkflowTrim struct {
	MediaID      string   `json:"mediaId"`
	StartSeconds *float64 `json:"startSeconds"`
	EndSeconds   *float64 `json:"endSeconds"`
}

type flowWorkflowTrimResult struct {
	WorkflowID   string  `json:"workflowId"`
	MediaID      string  `json:"mediaId"`
	StartSeconds float64 `json:"startSeconds"`
	EndSeconds   float64 `json:"endSeconds"`
}

type flowWorkflowBatch struct {
	WorkflowIDs []string `json:"workflowIds"`
	Archived    *bool    `json:"archived"`
}

type flowWorkflowBatchResult struct {
	WorkflowIDs []string `json:"workflowIds"`
	Archived    bool     `json:"archived"`
}

func decodeFlowWorkflow(value any, project string) (flowWorkflowResource, error) {
	var result flowWorkflowResource
	result.ID, _ = jsonField(value, 0).(string)
	result.ProjectID, _ = jsonField(value, 4).(string)
	if !flowUUIDPattern.MatchString(result.ID) || result.ProjectID != project {
		return result, failure(502, "flow_workflow_identity_mismatch")
	}
	result.CollectionID, _ = jsonField(value, 1).(string)
	result.Title, _ = jsonField(value, 3, 0).(string)
	result.CreatedAt = flowTimestamp(jsonField(value, 3, 1))
	result.Archived = flowFlag(jsonField(value, 3, 2))
	result.Favorited = flowFlag(jsonField(value, 3, 3))
	result.PrimaryMediaID, _ = jsonField(value, 3, 4).(string)
	result.MediaIDs = []string{}
	return result, nil
}

func flowWorkflowMedia(contents any, project string) (map[string][]flowMediaResource, error) {
	result := map[string][]flowMediaResource{}
	for _, row := range flowContentRows(contents, 2) {
		media, err := decodeFlowMedia(row, project)
		if err != nil {
			return nil, err
		}
		result[media.WorkflowID] = append(result[media.WorkflowID], media)
	}
	return result, nil
}

func flowWorkflows(contents any, project string) ([]flowWorkflowResource, error) {
	media, err := flowWorkflowMedia(contents, project)
	if err != nil {
		return nil, err
	}
	rows := flowContentRows(contents, 1)
	result := make([]flowWorkflowResource, 0, len(rows))
	for _, row := range rows {
		workflow, err := decodeFlowWorkflow(row, project)
		if err != nil {
			return nil, err
		}
		for _, item := range media[workflow.ID] {
			workflow.MediaIDs = append(workflow.MediaIDs, item.ID)
		}
		result = append(result, workflow)
	}
	return result, nil
}

func flowFindWorkflow(workflows []flowWorkflowResource, id string) (flowWorkflowResource, bool) {
	for _, workflow := range workflows {
		if workflow.ID == id {
			return workflow, true
		}
	}
	return flowWorkflowResource{}, false
}

func (service *service) flowWorkflowHTTP(ctx context.Context, record storageRecord, project, tail string, request flowHTTPRequest) (httpResponse, error) {
	if tail == "" && request.Method == http.MethodGet {
		return service.listFlowWorkflows(ctx, record, project, request)
	}
	if tail == ":batchArchive" && request.Method == http.MethodPost {
		return service.batchArchiveFlowWorkflows(ctx, record, project, request.Body)
	}
	id, action, ok := flowSplitTail(tail)
	if !ok {
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	contents, err := service.flowProjectContents(ctx, record, project)
	if err != nil {
		return httpResponse{}, err
	}
	workflows, err := flowWorkflows(contents, project)
	if err != nil {
		return httpResponse{}, err
	}
	if request.Method == http.MethodPost && action == "copy" {
		return service.copyFlowWorkflow(ctx, record, project, id, workflows, contents, request.Body)
	}
	current, found := flowFindWorkflow(workflows, id)
	if !found {
		return httpResponse{}, failure(404, "flow_workflow_not_found")
	}
	archive := func(archived bool) (flowWorkflowResource, error) {
		return service.updateFlowWorkflow(ctx, record, project, current, contents, flowWorkflowUpdate{Archived: &archived})
	}
	switch {
	case request.Method == http.MethodGet && action == "":
		return flowJSON(http.StatusOK, current)
	case request.Method == http.MethodPatch && action == "":
		var input flowWorkflowUpdate
		if err := flowStrict("workflow", request.Body, &input); err != nil {
			return httpResponse{}, err
		}
		updated, err := service.updateFlowWorkflow(ctx, record, project, current, contents, input)
		if err != nil {
			return httpResponse{}, err
		}
		return flowJSON(http.StatusOK, updated)
	case request.Method == http.MethodDelete && action == "":
		if _, err := archive(true); err != nil {
			return httpResponse{}, err
		}
		return flowNoContent(), nil
	case request.Method == http.MethodPost && action == "restore":
		restored, err := archive(false)
		if err != nil {
			return httpResponse{}, err
		}
		return flowJSON(http.StatusOK, restored)
	case request.Method == http.MethodPost && action == "purge":
		if !current.Archived {
			return httpResponse{}, failure(409, "flow_workflow_not_archived")
		}
		assets := flowAssetDeletion{WorkflowIDs: []string{current.ID}}
		if current.PrimaryMediaID != "" {
			assets.MediaIDs = []string{current.PrimaryMediaID}
		}
		if err := service.deleteFlowAssets(ctx, record, project, assets); err != nil {
			return httpResponse{}, err
		}
		return flowNoContent(), nil
	case request.Method == http.MethodPost && action == "trim":
		return service.trimFlowWorkflowVideo(ctx, record, project, current, contents, request.Body)
	}
	return httpResponse{}, failure(404, "flow_route_not_found")
}

func (service *service) listFlowWorkflows(ctx context.Context, record storageRecord, project string, request flowHTTPRequest) (httpResponse, error) {
	archived, err := flowArchivedFilter(request.Query)
	if err != nil {
		return httpResponse{}, err
	}
	contents, err := service.flowProjectContents(ctx, record, project)
	if err != nil {
		return httpResponse{}, err
	}
	workflows, err := flowWorkflows(contents, project)
	if err != nil {
		return httpResponse{}, err
	}
	collections, filterCollection := request.Query["collectionId"]
	result := flowWorkflowList{Workflows: []flowWorkflowResource{}}
	for _, workflow := range workflows {
		if workflow.Archived != archived || !flowTitleMatches(workflow.Title, request.Query.Get("search")) ||
			filterCollection && (len(collections) == 0 || workflow.CollectionID != collections[0]) {
			continue
		}
		result.Workflows = append(result.Workflows, workflow)
	}
	return flowJSON(http.StatusOK, result)
}

func (service *service) updateFlowWorkflow(ctx context.Context, record storageRecord, project string, current flowWorkflowResource, contents any, input flowWorkflowUpdate) (flowWorkflowResource, error) {
	metadata := make([]any, 5)
	var paths []string
	var collection any
	if input.Title != nil {
		title, err := flowResourceTitle(*input.Title)
		if err != nil {
			return current, err
		}
		metadata[0] = title
		paths = append(paths, "metadata.display_name")
	}
	if input.Archived != nil {
		metadata[2] = *input.Archived
		paths = append(paths, "metadata.archived")
	}
	if input.Favorited != nil {
		metadata[3] = *input.Favorited
		paths = append(paths, "metadata.favorited")
	}
	if input.PrimaryMediaID != nil {
		if *input.PrimaryMediaID == "" || !slices.Contains(current.MediaIDs, *input.PrimaryMediaID) {
			return current, failure(404, "flow_media_not_found")
		}
		metadata[4] = *input.PrimaryMediaID
		paths = append(paths, "metadata.primary_media_id")
	}
	if input.CollectionID != nil {
		if *input.CollectionID != "" {
			collections, err := flowCollections(contents, project)
			if err != nil {
				return current, err
			}
			if _, found := flowFindCollection(collections, *input.CollectionID); !found {
				return current, failure(404, "flow_collection_not_found")
			}
		}
		collection = *input.CollectionID
		paths = append(paths, "collection_id")
	}
	if len(paths) == 0 {
		return current, failure(400, "flow_workflow_request_invalid")
	}
	payload, err := service.flowProjectRPC(ctx, record, project, "mYWVGd", []any{
		[]any{current.ID, collection, nil, flowWireRow(metadata...), project}, flowMask(paths...),
	})
	if err != nil {
		return current, err
	}
	updated, err := decodeFlowWorkflow(payload, project)
	if err == nil && updated.ID != current.ID {
		err = failure(502, "flow_workflow_identity_mismatch")
	}
	updated.MediaIDs = current.MediaIDs
	return updated, err
}

func (service *service) batchArchiveFlowWorkflows(ctx context.Context, record storageRecord, project string, body []byte) (httpResponse, error) {
	var input flowWorkflowBatch
	if err := flowStrict("workflow_batch", body, &input); err != nil {
		return httpResponse{}, err
	}
	if input.Archived == nil || len(input.WorkflowIDs) == 0 {
		return httpResponse{}, failure(400, "flow_resource_ids_invalid")
	}
	if err := flowUniqueIdentifiers(input.WorkflowIDs, flowResourceBatchLimit); err != nil {
		return httpResponse{}, err
	}
	contents, err := service.flowProjectContents(ctx, record, project)
	if err != nil {
		return httpResponse{}, err
	}
	workflows, err := flowWorkflows(contents, project)
	if err != nil {
		return httpResponse{}, err
	}
	rows := make([]any, len(input.WorkflowIDs))
	for index, id := range input.WorkflowIDs {
		if _, found := flowFindWorkflow(workflows, id); !found {
			return httpResponse{}, failure(404, "flow_workflow_not_found")
		}
		rows[index] = []any{id, nil, nil, []any{nil, nil, *input.Archived}, project}
	}
	if _, err := service.flowProjectRPC(ctx, record, project, "pGCYOe", []any{rows, flowMask("metadata.archived")}); err != nil {
		return httpResponse{}, err
	}
	return flowJSON(http.StatusOK, flowWorkflowBatchResult{WorkflowIDs: input.WorkflowIDs, Archived: *input.Archived})
}

// The copy request carries two client-chosen seeds, one for the new workflow
// and one for its first media, exactly as Flow's web client sends them.
func (service *service) copyFlowWorkflow(ctx context.Context, record storageRecord, project, id string, workflows []flowWorkflowResource, contents any, body []byte) (httpResponse, error) {
	var input flowWorkflowCopy
	if err := flowStrict("workflow_copy", body, &input); err != nil {
		return httpResponse{}, err
	}
	source := project
	if input.SourceProjectID != "" {
		if !flowUUIDPattern.MatchString(input.SourceProjectID) {
			return httpResponse{}, failure(400, "flow_workflow_request_invalid")
		}
		source = input.SourceProjectID
	}
	if source != project {
		sourceContents, err := service.flowProjectContents(ctx, record, source)
		if err != nil {
			return httpResponse{}, err
		}
		if workflows, err = flowWorkflows(sourceContents, source); err != nil {
			return httpResponse{}, err
		}
	}
	if _, found := flowFindWorkflow(workflows, id); !found {
		return httpResponse{}, failure(404, "flow_workflow_not_found")
	}
	var collection any
	if input.CollectionID != "" {
		collections, err := flowCollections(contents, project)
		if err != nil {
			return httpResponse{}, err
		}
		if _, found := flowFindCollection(collections, input.CollectionID); !found {
			return httpResponse{}, failure(404, "flow_collection_not_found")
		}
		collection = input.CollectionID
	}
	payload, err := service.flowProjectRPC(ctx, record, project, "XKxtXb", flowWireRow(
		[]any{id, nil, nil, nil, source}, project, collection, nil, flowID(), flowID(),
	))
	if err != nil {
		return httpResponse{}, err
	}
	copied, err := decodeFlowWorkflow(jsonField(payload, 0), project)
	if err != nil {
		return httpResponse{}, err
	}
	rows, _ := jsonField(payload, 1).([]any)
	for _, row := range rows {
		media, err := decodeFlowMedia(row, project)
		if err != nil {
			return httpResponse{}, err
		}
		copied.MediaIDs = append(copied.MediaIDs, media.ID)
	}
	return flowJSON(http.StatusCreated, copied)
}

func (service *service) trimFlowWorkflowVideo(ctx context.Context, record storageRecord, project string, current flowWorkflowResource, contents any, body []byte) (httpResponse, error) {
	var input flowWorkflowTrim
	if err := flowStrict("workflow_trim", body, &input); err != nil {
		return httpResponse{}, err
	}
	if input.StartSeconds == nil || input.EndSeconds == nil || *input.StartSeconds < 0 || *input.EndSeconds <= *input.StartSeconds ||
		math.IsInf(*input.EndSeconds, 0) || math.IsNaN(*input.StartSeconds) || math.IsNaN(*input.EndSeconds) {
		return httpResponse{}, failure(400, "flow_trim_range_invalid")
	}
	media, err := flowWorkflowMedia(contents, project)
	if err != nil {
		return httpResponse{}, err
	}
	found := false
	for _, item := range media[current.ID] {
		found = found || item.ID == input.MediaID && item.Type == "video"
	}
	if !found {
		return httpResponse{}, failure(404, "flow_media_not_found")
	}
	if _, err := service.flowProjectRPC(ctx, record, project, "iVqlKd", []any{
		input.MediaID, flowDurationWire(*input.StartSeconds), flowDurationWire(*input.EndSeconds),
	}); err != nil {
		return httpResponse{}, err
	}
	return flowJSON(http.StatusOK, flowWorkflowTrimResult{
		WorkflowID: current.ID, MediaID: input.MediaID, StartSeconds: *input.StartSeconds, EndSeconds: *input.EndSeconds,
	})
}
