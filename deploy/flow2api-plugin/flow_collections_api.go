package main

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const flowResourceBatchLimit = 100

// Flow stores the collection id the client chooses, so the id is generated here.
var newFlowCollectionID = func() string { return strings.ToLower(flowID()) }

type flowCollectionResource struct {
	ID                string `json:"id"`
	ProjectID         string `json:"projectId"`
	ParentID          string `json:"parentId,omitempty"`
	Title             string `json:"title"`
	Archived          bool   `json:"archived"`
	Favorited         bool   `json:"favorited"`
	ThumbnailMediaKey string `json:"thumbnailMediaKey,omitempty"`
}

type flowCollectionList struct {
	Collections []flowCollectionResource `json:"collections"`
}

type flowCollectionCreate struct {
	Title    string `json:"title"`
	ParentID string `json:"parentId"`
}

type flowCollectionUpdate struct {
	Title             *string `json:"title"`
	Favorited         *bool   `json:"favorited"`
	Archived          *bool   `json:"archived"`
	ParentID          *string `json:"parentId"`
	ThumbnailMediaKey *string `json:"thumbnailMediaKey"`
}

type flowCollectionItems struct {
	WorkflowIDs   []string `json:"workflowIds"`
	CollectionIDs []string `json:"collectionIds"`
	SceneIDs      []string `json:"sceneIds"`
}

type flowCollectionMembership struct {
	CollectionID  string   `json:"collectionId"`
	WorkflowIDs   []string `json:"workflowIds"`
	CollectionIDs []string `json:"collectionIds"`
	SceneIDs      []string `json:"sceneIds"`
}

func flowFlag(value any) bool { return value == true || value == float64(1) }

func decodeFlowCollection(value any, project string) (flowCollectionResource, error) {
	var result flowCollectionResource
	result.ID, _ = jsonField(value, 0).(string)
	result.ParentID, _ = jsonField(value, 1).(string)
	result.ProjectID, _ = jsonField(value, 3).(string)
	if !flowIdentifier(result.ID) || result.ProjectID != project {
		return result, failure(502, "flow_collection_identity_mismatch")
	}
	result.Title, _ = jsonField(value, 2, 0).(string)
	result.ThumbnailMediaKey, _ = jsonField(value, 2, 1).(string)
	result.Archived = flowFlag(jsonField(value, 2, 2))
	result.Favorited = flowFlag(jsonField(value, 2, 4))
	return result, nil
}

func flowCollections(contents any, project string) ([]flowCollectionResource, error) {
	rows := flowContentRows(contents, 0)
	result := make([]flowCollectionResource, 0, len(rows))
	for _, row := range rows {
		collection, err := decodeFlowCollection(row, project)
		if err != nil {
			return nil, err
		}
		result = append(result, collection)
	}
	return result, nil
}

func flowFindCollection(collections []flowCollectionResource, id string) (flowCollectionResource, bool) {
	for _, collection := range collections {
		if collection.ID == id {
			return collection, true
		}
	}
	return flowCollectionResource{}, false
}

func flowCollectionWithin(collections []flowCollectionResource, from, target string) bool {
	for steps := 0; from != "" && steps <= len(collections); steps++ {
		if from == target {
			return true
		}
		collection, _ := flowFindCollection(collections, from)
		from = collection.ParentID
	}
	return false
}

func flowResourceTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if !utf8.ValidString(title) || title == "" || utf8.RuneCountInString(title) > 256 || strings.ContainsFunc(title, unicode.IsControl) {
		return "", failure(400, "flow_title_invalid")
	}
	return title, nil
}

func flowContentRows(contents any, index int) []any {
	rows, _ := jsonField(contents, index).([]any)
	return rows
}

func flowArchivedFilter(query url.Values) (bool, error) {
	switch query.Get("archived") {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	}
	return false, failure(400, "flow_archived_filter_invalid")
}

func flowTitleMatches(title, search string) bool {
	return search == "" || strings.Contains(strings.ToLower(title), strings.ToLower(search))
}

func flowSplitTail(tail string) (id, action string, ok bool) {
	id, action, _ = strings.Cut(tail, ":")
	return id, action, flowIdentifier(id) && !strings.Contains(id, "/")
}

// flowWireRow drops trailing unset fields the way Flow's own client omits them.
func flowWireRow(values ...any) []any {
	end := len(values)
	for end > 0 && values[end-1] == nil {
		end--
	}
	if end == 0 {
		return nil
	}
	return values[:end]
}

func flowIDsOrNil(ids []string) any {
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// An update mask is one message whose repeated field lists the paths.
func flowMask(paths ...string) []any {
	items := make([]any, len(paths))
	for index, path := range paths {
		items[index] = path
	}
	return []any{items}
}

func flowNoContent() httpResponse {
	return httpResponse{StatusCode: http.StatusNoContent, Headers: http.Header{"Cache-Control": {"private, no-store"}}}
}

func flowUniqueIdentifiers(values []string, limit int) error {
	seen := map[string]bool{}
	for _, value := range values {
		if !flowIdentifier(value) || strings.Contains(value, "/") || strings.Contains(value, ":") || seen[value] {
			return failure(400, "flow_resource_ids_invalid")
		}
		seen[value] = true
	}
	if len(values) > limit {
		return failure(400, "flow_resource_ids_invalid")
	}
	return nil
}

func flowContains(value any, target string) bool {
	switch typed := value.(type) {
	case string:
		return typed == target
	case []any:
		for _, item := range typed {
			if flowContains(item, target) {
				return true
			}
		}
	}
	return false
}

func flowWholeNumber(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), typed == math.Trunc(typed)
	case string:
		number, err := strconv.ParseInt(typed, 10, 64)
		return number, err == nil
	}
	return 0, false
}

func flowTimestamp(value any) string {
	seconds, ok := flowWholeNumber(jsonField(value, 0))
	if !ok {
		return ""
	}
	nanos, _ := flowWholeNumber(jsonField(value, 1))
	return time.Unix(seconds, nanos).UTC().Format(time.RFC3339Nano)
}

func (service *service) flowProjectRPC(ctx context.Context, record storageRecord, project, rpcID string, args any) (any, error) {
	var payload any
	err := service.withFlowSession(ctx, record, func(session *flowSession) error {
		var err error
		payload, err = session.rpc(ctx, rpcID, args, "/project/"+project, "")
		return err
	})
	return payload, err
}

type flowAssetDeletion struct {
	CollectionIDs, WorkflowIDs, SceneIDs, MediaIDs []string
}

// Permanent deletion; callers only reach it for resources already in the trash.
func (service *service) deleteFlowAssets(ctx context.Context, record storageRecord, project string, assets flowAssetDeletion) error {
	_, err := service.flowProjectRPC(ctx, record, project, "cz8Z4b", flowWireRow(
		flowIDsOrNil(assets.CollectionIDs), flowIDsOrNil(assets.WorkflowIDs), project, nil,
		flowIDsOrNil(assets.SceneIDs), nil, flowIDsOrNil(assets.MediaIDs),
	))
	return err
}

func (service *service) flowCollectionHTTP(ctx context.Context, record storageRecord, project, tail string, request flowHTTPRequest) (httpResponse, error) {
	if tail == "" {
		switch request.Method {
		case http.MethodGet:
			return service.listFlowCollections(ctx, record, project, request)
		case http.MethodPost:
			return service.createFlowCollection(ctx, record, project, request.Body)
		}
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	id, action, ok := flowSplitTail(tail)
	if !ok {
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	contents, err := service.flowProjectContents(ctx, record, project)
	if err != nil {
		return httpResponse{}, err
	}
	collections, err := flowCollections(contents, project)
	if err != nil {
		return httpResponse{}, err
	}
	current, found := flowFindCollection(collections, id)
	if !found {
		return httpResponse{}, failure(404, "flow_collection_not_found")
	}
	archive := func(archived bool) (flowCollectionResource, error) {
		return service.updateFlowCollection(ctx, record, project, current, collections, flowCollectionUpdate{Archived: &archived})
	}
	switch {
	case request.Method == http.MethodGet && action == "":
		return flowJSON(http.StatusOK, current)
	case request.Method == http.MethodPatch && action == "":
		var input flowCollectionUpdate
		if err := flowStrict("collection", request.Body, &input); err != nil {
			return httpResponse{}, err
		}
		updated, err := service.updateFlowCollection(ctx, record, project, current, collections, input)
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
		return service.purgeFlowCollection(ctx, record, project, current, collections, contents)
	case request.Method == http.MethodPost && (action == "addItems" || action == "removeItems"):
		var items flowCollectionItems
		if err := flowStrict("collection_items", request.Body, &items); err != nil {
			return httpResponse{}, err
		}
		destination := id
		if action == "removeItems" {
			destination = ""
		}
		return service.moveFlowCollectionItems(ctx, record, project, destination, items, collections, contents)
	}
	return httpResponse{}, failure(404, "flow_route_not_found")
}

func (service *service) listFlowCollections(ctx context.Context, record storageRecord, project string, request flowHTTPRequest) (httpResponse, error) {
	archived, err := flowArchivedFilter(request.Query)
	if err != nil {
		return httpResponse{}, err
	}
	contents, err := service.flowProjectContents(ctx, record, project)
	if err != nil {
		return httpResponse{}, err
	}
	collections, err := flowCollections(contents, project)
	if err != nil {
		return httpResponse{}, err
	}
	parents, filterParent := request.Query["parentId"]
	result := flowCollectionList{Collections: []flowCollectionResource{}}
	for _, collection := range collections {
		if collection.Archived != archived || !flowTitleMatches(collection.Title, request.Query.Get("search")) ||
			filterParent && (len(parents) == 0 || collection.ParentID != parents[0]) {
			continue
		}
		result.Collections = append(result.Collections, collection)
	}
	return flowJSON(http.StatusOK, result)
}

func (service *service) createFlowCollection(ctx context.Context, record storageRecord, project string, body []byte) (httpResponse, error) {
	var input flowCollectionCreate
	if err := flowStrict("collection", body, &input); err != nil {
		return httpResponse{}, err
	}
	title, err := flowResourceTitle(input.Title)
	if err != nil {
		return httpResponse{}, err
	}
	contents, err := service.flowProjectContents(ctx, record, project)
	if err != nil {
		return httpResponse{}, err
	}
	collections, err := flowCollections(contents, project)
	if err != nil {
		return httpResponse{}, err
	}
	var parent any
	if input.ParentID != "" {
		if _, found := flowFindCollection(collections, input.ParentID); !found {
			return httpResponse{}, failure(404, "flow_collection_not_found")
		}
		parent = input.ParentID
	}
	id := newFlowCollectionID()
	payload, err := service.flowProjectRPC(ctx, record, project, "Uxbujd", []any{"projects/" + project, []any{id, parent, []any{title}, project}})
	if err != nil {
		return httpResponse{}, err
	}
	created, err := decodeFlowCollection(payload, project)
	if err != nil {
		return httpResponse{}, err
	}
	if created.ID != id {
		return httpResponse{}, failure(502, "flow_collection_identity_mismatch")
	}
	return flowJSON(http.StatusCreated, created)
}

func (service *service) updateFlowCollection(ctx context.Context, record storageRecord, project string, current flowCollectionResource, collections []flowCollectionResource, input flowCollectionUpdate) (flowCollectionResource, error) {
	metadata := make([]any, 5)
	var paths []string
	var parent any
	if input.Title != nil {
		title, err := flowResourceTitle(*input.Title)
		if err != nil {
			return current, err
		}
		metadata[0] = title
		paths = append(paths, "metadata.display_name")
	}
	if input.ThumbnailMediaKey != nil {
		if !flowIdentifier(*input.ThumbnailMediaKey) {
			return current, failure(400, "flow_collection_request_invalid")
		}
		metadata[1] = *input.ThumbnailMediaKey
		paths = append(paths, "metadata.thumbnail_media_key")
	}
	if input.Archived != nil {
		metadata[2] = *input.Archived
		paths = append(paths, "metadata.archived")
	}
	if input.Favorited != nil {
		metadata[4] = *input.Favorited
		paths = append(paths, "metadata.favorited")
	}
	if input.ParentID != nil {
		if *input.ParentID != "" {
			if _, found := flowFindCollection(collections, *input.ParentID); !found {
				return current, failure(404, "flow_collection_not_found")
			}
			if flowCollectionWithin(collections, *input.ParentID, current.ID) {
				return current, failure(409, "flow_collection_cycle")
			}
		}
		parent = *input.ParentID
		paths = append(paths, "parent_collection_id")
	}
	if len(paths) == 0 {
		return current, failure(400, "flow_collection_request_invalid")
	}
	payload, err := service.flowProjectRPC(ctx, record, project, "O2COMe", []any{
		[]any{current.ID, parent, flowWireRow(metadata...), project}, flowMask(paths...),
	})
	if err != nil {
		return current, err
	}
	updated, err := decodeFlowCollection(payload, project)
	if err == nil && updated.ID != current.ID {
		err = failure(502, "flow_collection_identity_mismatch")
	}
	return updated, err
}

func (service *service) purgeFlowCollection(ctx context.Context, record storageRecord, project string, current flowCollectionResource, collections []flowCollectionResource, contents any) (httpResponse, error) {
	if !current.Archived {
		return httpResponse{}, failure(409, "flow_collection_not_archived")
	}
	occupied := flowContains(flowContentRows(contents, 5), current.ID)
	for _, collection := range collections {
		occupied = occupied || collection.ParentID == current.ID
	}
	for _, row := range flowContentRows(contents, 1) {
		occupied = occupied || jsonField(row, 1) == current.ID
	}
	for _, row := range flowContentRows(contents, 4) {
		occupied = occupied || jsonField(row, 6) == current.ID
	}
	if occupied {
		return httpResponse{}, failure(409, "flow_collection_not_empty")
	}
	if err := service.deleteFlowAssets(ctx, record, project, flowAssetDeletion{CollectionIDs: []string{current.ID}}); err != nil {
		return httpResponse{}, err
	}
	return flowNoContent(), nil
}

func (service *service) moveFlowCollectionItems(ctx context.Context, record storageRecord, project, destination string, items flowCollectionItems, collections []flowCollectionResource, contents any) (httpResponse, error) {
	if len(items.WorkflowIDs)+len(items.CollectionIDs)+len(items.SceneIDs) == 0 {
		return httpResponse{}, failure(400, "flow_resource_ids_invalid")
	}
	for _, ids := range [][]string{items.WorkflowIDs, items.CollectionIDs, items.SceneIDs} {
		if err := flowUniqueIdentifiers(ids, flowResourceBatchLimit); err != nil {
			return httpResponse{}, err
		}
	}
	workflows, err := flowWorkflows(contents, project)
	if err != nil {
		return httpResponse{}, err
	}
	scenes, err := flowScenes(contents, project)
	if err != nil {
		return httpResponse{}, err
	}
	for _, id := range items.WorkflowIDs {
		if _, found := flowFindWorkflow(workflows, id); !found {
			return httpResponse{}, failure(404, "flow_workflow_not_found")
		}
	}
	for _, id := range items.SceneIDs {
		if _, found := flowFindScene(scenes, id); !found {
			return httpResponse{}, failure(404, "flow_scene_not_found")
		}
	}
	for _, id := range items.CollectionIDs {
		if _, found := flowFindCollection(collections, id); !found {
			return httpResponse{}, failure(404, "flow_collection_not_found")
		}
		if flowCollectionWithin(collections, destination, id) {
			return httpResponse{}, failure(409, "flow_collection_cycle")
		}
	}
	var target, root any
	if destination == "" {
		root = []any{}
	} else {
		target = []any{destination}
	}
	_, err = service.flowProjectRPC(ctx, record, project, "kVdhHf", flowWireRow(
		project, target, flowIDsOrNil(items.WorkflowIDs), flowIDsOrNil(items.CollectionIDs), nil, root, nil, flowIDsOrNil(items.SceneIDs),
	))
	if err != nil {
		return httpResponse{}, err
	}
	result := flowCollectionMembership{CollectionID: destination, WorkflowIDs: []string{}, CollectionIDs: []string{}, SceneIDs: []string{}}
	result.WorkflowIDs = append(result.WorkflowIDs, items.WorkflowIDs...)
	result.CollectionIDs = append(result.CollectionIDs, items.CollectionIDs...)
	result.SceneIDs = append(result.SceneIDs, items.SceneIDs...)
	return flowJSON(http.StatusOK, result)
}
