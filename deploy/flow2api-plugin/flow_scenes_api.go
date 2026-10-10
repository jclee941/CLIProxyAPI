package main

import (
	"context"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

const flowSceneAspectPortrait, flowSceneAspectLandscape = 1, 2

const flowSceneDefaultClipSeconds = 8

type flowSceneResource struct {
	ID                string `json:"id"`
	ProjectID         string `json:"projectId"`
	Title             string `json:"title"`
	AspectRatio       string `json:"aspectRatio,omitempty"`
	CollectionID      string `json:"collectionId,omitempty"`
	ThumbnailMediaKey string `json:"thumbnailMediaKey,omitempty"`
	Archived          bool   `json:"archived"`
	Favorited         bool   `json:"favorited"`
	CreatedAt         string `json:"createdAt,omitempty"`
	UpdatedAt         string `json:"updatedAt,omitempty"`
}

type flowSceneClip struct {
	Position        int     `json:"position"`
	WorkflowID      string  `json:"workflowId"`
	PrimaryMediaID  string  `json:"primaryMediaId,omitempty"`
	DurationSeconds float64 `json:"durationSeconds"`
	StartSeconds    float64 `json:"startSeconds"`
	EndSeconds      float64 `json:"endSeconds"`
}

type flowSceneDetail struct {
	flowSceneResource
	Clips []flowSceneClip `json:"clips"`
}

type flowSceneList struct {
	Scenes []flowSceneResource `json:"scenes"`
}

type flowSceneClipList struct {
	Clips []flowSceneClip `json:"clips"`
}

type flowSceneCreate struct {
	AspectRatio  string   `json:"aspectRatio"`
	CollectionID string   `json:"collectionId"`
	WorkflowIDs  []string `json:"workflowIds"`
}

type flowSceneUpdate struct {
	Title        *string `json:"title"`
	Favorited    *bool   `json:"favorited"`
	Archived     *bool   `json:"archived"`
	CollectionID *string `json:"collectionId"`
	AspectRatio  *string `json:"aspectRatio"`
}

type flowSceneCopy struct {
	SourceProjectID string `json:"sourceProjectId"`
	CollectionID    string `json:"collectionId"`
}

type flowSceneClipAdd struct {
	WorkflowIDs  []string `json:"workflowIds"`
	Position     *int     `json:"position"`
	StartSeconds *float64 `json:"startSeconds"`
	EndSeconds   *float64 `json:"endSeconds"`
}

type flowSceneClipUpdate struct {
	StartSeconds *float64 `json:"startSeconds"`
	EndSeconds   *float64 `json:"endSeconds"`
}

type flowSceneClipMove struct {
	From *int `json:"from"`
	To   *int `json:"to"`
}

func flowSceneAspectEnum(name string) (int, bool) {
	switch name {
	case "9:16":
		return flowSceneAspectPortrait, true
	case "16:9":
		return flowSceneAspectLandscape, true
	}
	return 0, false
}

func flowSceneAspectName(value any) string {
	switch number, _ := jsonInteger(value); number {
	case flowSceneAspectPortrait:
		return "9:16"
	case flowSceneAspectLandscape:
		return "16:9"
	}
	return ""
}

func decodeFlowScene(value any, project string) (flowSceneResource, error) {
	var result flowSceneResource
	result.ID, _ = jsonField(value, 0).(string)
	if !flowIdentifier(result.ID) {
		return result, failure(502, "flow_scene_response_invalid")
	}
	result.ProjectID = project
	result.Title, _ = jsonField(value, 1).(string)
	result.ThumbnailMediaKey, _ = jsonField(value, 2).(string)
	result.CreatedAt = flowTimestamp(jsonField(value, 3))
	result.UpdatedAt = flowTimestamp(jsonField(value, 4))
	result.AspectRatio = flowSceneAspectName(jsonField(value, 5))
	result.CollectionID, _ = jsonField(value, 6).(string)
	result.Archived = flowFlag(jsonField(value, 7, 0))
	result.Favorited = flowFlag(jsonField(value, 7, 1))
	return result, nil
}

func flowScenes(contents any, project string) ([]flowSceneResource, error) {
	rows := flowContentRows(contents, 4)
	result := make([]flowSceneResource, 0, len(rows))
	for _, row := range rows {
		scene, err := decodeFlowScene(row, project)
		if err != nil {
			return nil, err
		}
		result = append(result, scene)
	}
	return result, nil
}

func flowFindScene(scenes []flowSceneResource, id string) (flowSceneResource, bool) {
	for _, scene := range scenes {
		if scene.ID == id {
			return scene, true
		}
	}
	return flowSceneResource{}, false
}

// A duration is a seconds/nanos message; 64-bit seconds may arrive as strings.
func flowDurationSeconds(value any) (float64, bool) {
	list, ok := value.([]any)
	if !ok {
		return 0, false
	}
	whole, _ := flowWholeNumber(jsonField(list, 0))
	nanos, _ := flowWholeNumber(jsonField(list, 1))
	return float64(whole) + float64(nanos)/1e9, true
}

func flowDurationWire(seconds float64) []any {
	whole := math.Floor(seconds)
	nanos := int(math.Round((seconds - whole) * 1e9))
	if nanos >= 1e9 {
		whole, nanos = whole+1, 0
	}
	if nanos == 0 {
		return []any{int(whole)}
	}
	return []any{int(whole), nanos}
}

func decodeFlowSceneClips(payload any) []flowSceneClip {
	rows, _ := jsonField(payload, 0).([]any)
	clips := make([]flowSceneClip, 0, len(rows))
	for _, row := range rows {
		workflow, _ := jsonField(row, 0, 0).(string)
		if workflow == "" {
			continue
		}
		clip := flowSceneClip{WorkflowID: workflow}
		clip.Position, _ = jsonInteger(jsonField(row, 2, 0))
		clip.PrimaryMediaID, _ = jsonField(row, 0, 3, 4).(string)
		clip.DurationSeconds, _ = flowDurationSeconds(jsonField(row, 2, 1))
		if clip.DurationSeconds == 0 {
			clip.DurationSeconds = flowSceneDefaultClipSeconds
		}
		clip.StartSeconds, _ = flowDurationSeconds(jsonField(row, 2, 2))
		clip.EndSeconds, _ = flowDurationSeconds(jsonField(row, 2, 3))
		if clip.EndSeconds == 0 {
			clip.EndSeconds = clip.DurationSeconds
		}
		clips = append(clips, clip)
	}
	slices.SortStableFunc(clips, func(a, b flowSceneClip) int { return a.Position - b.Position })
	return clips
}

func (service *service) readFlowSceneClips(ctx context.Context, record storageRecord, project, scene string) ([]flowSceneClip, error) {
	payload, err := service.flowProjectRPC(ctx, record, project, "uwAyfb", []any{scene, project})
	if err != nil {
		return nil, err
	}
	return decodeFlowSceneClips(payload), nil
}

// UpdateSceneWorkflows replaces the scene's whole clip list; reordering and
// trimming are edits of that list.
func (service *service) writeFlowSceneClips(ctx context.Context, record storageRecord, project, scene string, clips []flowSceneClip) ([]flowSceneClip, error) {
	rows := make([]any, len(clips))
	for index, clip := range clips {
		metadata := []any{}
		if clip.PrimaryMediaID != "" {
			metadata = []any{nil, nil, nil, nil, clip.PrimaryMediaID}
		}
		rows[index] = []any{
			[]any{clip.WorkflowID, nil, nil, metadata}, scene,
			[]any{index, flowDurationWire(clip.DurationSeconds), flowDurationWire(clip.StartSeconds), flowDurationWire(clip.EndSeconds)},
		}
	}
	payload, err := service.flowProjectRPC(ctx, record, project, "GoMJte", []any{project, scene, rows})
	if err != nil {
		return nil, err
	}
	return decodeFlowSceneClips(payload), nil
}

func (service *service) flowSceneHTTP(ctx context.Context, record storageRecord, project, tail string, request flowHTTPRequest) (httpResponse, error) {
	if tail == "" {
		switch request.Method {
		case http.MethodGet:
			return service.listFlowScenes(ctx, record, project, request)
		case http.MethodPost:
			return service.createFlowScene(ctx, record, project, request.Body)
		}
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	if id, rest, nested := strings.Cut(tail, "/"); nested {
		return service.flowSceneClipHTTP(ctx, record, project, id, rest, request)
	}
	id, action, ok := flowSplitTail(tail)
	if !ok {
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	contents, err := service.flowProjectContents(ctx, record, project)
	if err != nil {
		return httpResponse{}, err
	}
	scenes, err := flowScenes(contents, project)
	if err != nil {
		return httpResponse{}, err
	}
	if request.Method == http.MethodPost && action == "copy" {
		return service.copyFlowScene(ctx, record, project, id, scenes, contents, request.Body)
	}
	current, found := flowFindScene(scenes, id)
	if !found {
		return httpResponse{}, failure(404, "flow_scene_not_found")
	}
	archive := func(archived bool) (flowSceneResource, error) {
		return service.updateFlowScene(ctx, record, project, current, contents, flowSceneUpdate{Archived: &archived})
	}
	switch {
	case request.Method == http.MethodGet && action == "":
		clips, err := service.readFlowSceneClips(ctx, record, project, id)
		if err != nil {
			return httpResponse{}, err
		}
		return flowJSON(http.StatusOK, flowSceneDetail{flowSceneResource: current, Clips: clips})
	case request.Method == http.MethodPatch && action == "":
		var input flowSceneUpdate
		if err := flowStrict("scene", request.Body, &input); err != nil {
			return httpResponse{}, err
		}
		updated, err := service.updateFlowScene(ctx, record, project, current, contents, input)
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
			return httpResponse{}, failure(409, "flow_scene_not_archived")
		}
		if err := service.deleteFlowAssets(ctx, record, project, flowAssetDeletion{SceneIDs: []string{current.ID}}); err != nil {
			return httpResponse{}, err
		}
		return flowNoContent(), nil
	}
	return httpResponse{}, failure(404, "flow_route_not_found")
}

func (service *service) listFlowScenes(ctx context.Context, record storageRecord, project string, request flowHTTPRequest) (httpResponse, error) {
	archived, err := flowArchivedFilter(request.Query)
	if err != nil {
		return httpResponse{}, err
	}
	contents, err := service.flowProjectContents(ctx, record, project)
	if err != nil {
		return httpResponse{}, err
	}
	scenes, err := flowScenes(contents, project)
	if err != nil {
		return httpResponse{}, err
	}
	collections, filterCollection := request.Query["collectionId"]
	result := flowSceneList{Scenes: []flowSceneResource{}}
	for _, scene := range scenes {
		if scene.Archived != archived || !flowTitleMatches(scene.Title, request.Query.Get("search")) ||
			filterCollection && (len(collections) == 0 || scene.CollectionID != collections[0]) {
			continue
		}
		result.Scenes = append(result.Scenes, scene)
	}
	return flowJSON(http.StatusOK, result)
}

func (service *service) createFlowScene(ctx context.Context, record storageRecord, project string, body []byte) (httpResponse, error) {
	var input flowSceneCreate
	if err := flowStrict("scene", body, &input); err != nil {
		return httpResponse{}, err
	}
	aspect, ok := flowSceneAspectEnum(input.AspectRatio)
	if !ok {
		return httpResponse{}, failure(400, "flow_scene_aspect_invalid")
	}
	if err := flowUniqueIdentifiers(input.WorkflowIDs, 50); err != nil {
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
	for _, id := range input.WorkflowIDs {
		if _, found := flowFindWorkflow(workflows, id); !found {
			return httpResponse{}, failure(404, "flow_workflow_not_found")
		}
	}
	collection, err := flowDestinationCollection(contents, project, input.CollectionID)
	if err != nil {
		return httpResponse{}, err
	}
	payload, err := service.flowProjectRPC(ctx, record, project, "rqZuUc", flowWireRow(
		"projects/"+project, flowIDsOrNil(input.WorkflowIDs), collection, nil, aspect,
	))
	if err != nil {
		return httpResponse{}, err
	}
	scene, err := decodeFlowScene(jsonField(payload, 0), project)
	if err != nil {
		return httpResponse{}, err
	}
	detail := flowSceneDetail{flowSceneResource: scene, Clips: []flowSceneClip{}}
	if len(input.WorkflowIDs) > 0 {
		if detail.Clips, err = service.readFlowSceneClips(ctx, record, project, scene.ID); err != nil {
			return httpResponse{}, err
		}
	}
	return flowJSON(http.StatusCreated, detail)
}

func flowDestinationCollection(contents any, project, id string) (any, error) {
	if id == "" {
		return nil, nil
	}
	collections, err := flowCollections(contents, project)
	if err != nil {
		return nil, err
	}
	if _, found := flowFindCollection(collections, id); !found {
		return nil, failure(404, "flow_collection_not_found")
	}
	return id, nil
}

func (service *service) updateFlowScene(ctx context.Context, record storageRecord, project string, current flowSceneResource, contents any, input flowSceneUpdate) (flowSceneResource, error) {
	scene := make([]any, 8)
	scene[0] = current.ID
	var paths []string
	var metadata [2]any
	if input.Title != nil {
		title, err := flowResourceTitle(*input.Title)
		if err != nil {
			return current, err
		}
		scene[1] = title
		paths = append(paths, "display_name")
	}
	if input.AspectRatio != nil {
		aspect, ok := flowSceneAspectEnum(*input.AspectRatio)
		if !ok {
			return current, failure(400, "flow_scene_aspect_invalid")
		}
		scene[5] = aspect
		paths = append(paths, "aspect_ratio")
	}
	if input.CollectionID != nil {
		if *input.CollectionID != "" {
			if _, err := flowDestinationCollection(contents, project, *input.CollectionID); err != nil {
				return current, err
			}
		}
		scene[6] = *input.CollectionID
		paths = append(paths, "parent_collection_id")
	}
	if input.Archived != nil {
		metadata[0] = *input.Archived
		paths = append(paths, "scene_metadata.is_archived")
	}
	if input.Favorited != nil {
		metadata[1] = *input.Favorited
		paths = append(paths, "scene_metadata.is_favorited")
	}
	if len(paths) == 0 {
		return current, failure(400, "flow_scene_request_invalid")
	}
	if row := flowWireRow(metadata[:]...); row != nil {
		scene[7] = row
	}
	payload, err := service.flowProjectRPC(ctx, record, project, "BpMsoe", []any{project, current.ID, flowWireRow(scene...), flowMask(paths...)})
	if err != nil {
		return current, err
	}
	updated, err := decodeFlowScene(jsonField(payload, 0), project)
	if err == nil && updated.ID != current.ID {
		err = failure(502, "flow_scene_identity_mismatch")
	}
	return updated, err
}

func (service *service) copyFlowScene(ctx context.Context, record storageRecord, project, id string, scenes []flowSceneResource, contents any, body []byte) (httpResponse, error) {
	var input flowSceneCopy
	if err := flowStrict("scene_copy", body, &input); err != nil {
		return httpResponse{}, err
	}
	source := project
	if input.SourceProjectID != "" {
		if !flowUUIDPattern.MatchString(input.SourceProjectID) {
			return httpResponse{}, failure(400, "flow_scene_request_invalid")
		}
		source = input.SourceProjectID
	}
	if source != project {
		sourceContents, err := service.flowProjectContents(ctx, record, source)
		if err != nil {
			return httpResponse{}, err
		}
		if scenes, err = flowScenes(sourceContents, source); err != nil {
			return httpResponse{}, err
		}
	}
	if _, found := flowFindScene(scenes, id); !found {
		return httpResponse{}, failure(404, "flow_scene_not_found")
	}
	collection, err := flowDestinationCollection(contents, project, input.CollectionID)
	if err != nil {
		return httpResponse{}, err
	}
	payload, err := service.flowProjectRPC(ctx, record, project, "OSd63c", flowWireRow(source, id, collection, project))
	if err != nil {
		return httpResponse{}, err
	}
	copied, err := decodeFlowScene(jsonField(payload, 0), project)
	if err != nil {
		return httpResponse{}, err
	}
	detail := flowSceneDetail{flowSceneResource: copied, Clips: []flowSceneClip{}}
	if clips, _ := jsonField(payload, 1).([]any); len(clips) > 0 {
		if detail.Clips, err = service.readFlowSceneClips(ctx, record, project, copied.ID); err != nil {
			return httpResponse{}, err
		}
	}
	return flowJSON(http.StatusCreated, detail)
}

func (service *service) flowSceneClipHTTP(ctx context.Context, record storageRecord, project, id, rest string, request flowHTTPRequest) (httpResponse, error) {
	if !flowIdentifier(id) || strings.Contains(id, ":") {
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	position, positional := strings.CutPrefix(rest, "clips/")
	index, err := strconv.Atoi(position)
	if positional && (err != nil || index < 0) {
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	contents, err := service.flowProjectContents(ctx, record, project)
	if err != nil {
		return httpResponse{}, err
	}
	scenes, err := flowScenes(contents, project)
	if err != nil {
		return httpResponse{}, err
	}
	if _, found := flowFindScene(scenes, id); !found {
		return httpResponse{}, failure(404, "flow_scene_not_found")
	}
	switch {
	case rest == "clips" && request.Method == http.MethodGet:
		clips, err := service.readFlowSceneClips(ctx, record, project, id)
		if err != nil {
			return httpResponse{}, err
		}
		return flowJSON(http.StatusOK, flowSceneClipList{Clips: clips})
	case rest == "clips" && request.Method == http.MethodPost:
		return service.addFlowSceneClips(ctx, record, project, id, contents, request.Body)
	case rest == "clips:reorder" && request.Method == http.MethodPost:
		return service.editFlowSceneClips(ctx, record, project, id, func(clips []flowSceneClip) ([]flowSceneClip, error) {
			var input flowSceneClipMove
			if err := flowStrict("scene_clip_move", request.Body, &input); err != nil {
				return nil, err
			}
			if input.From == nil || input.To == nil || *input.From < 0 || *input.From >= len(clips) || *input.To < 0 || *input.To >= len(clips) {
				return nil, failure(400, "flow_clip_position_invalid")
			}
			moved := clips[*input.From]
			clips = slices.Delete(clips, *input.From, *input.From+1)
			return slices.Insert(clips, *input.To, moved), nil
		})
	case positional && request.Method == http.MethodPatch:
		return service.editFlowSceneClips(ctx, record, project, id, func(clips []flowSceneClip) ([]flowSceneClip, error) {
			var input flowSceneClipUpdate
			if err := flowStrict("scene_clip", request.Body, &input); err != nil {
				return nil, err
			}
			if index >= len(clips) {
				return nil, failure(404, "flow_clip_not_found")
			}
			clip := &clips[index]
			if input.StartSeconds != nil {
				clip.StartSeconds = *input.StartSeconds
			}
			if input.EndSeconds != nil {
				clip.EndSeconds = *input.EndSeconds
			}
			if input.StartSeconds == nil && input.EndSeconds == nil || clip.StartSeconds < 0 || clip.EndSeconds <= clip.StartSeconds || clip.EndSeconds > clip.DurationSeconds {
				return nil, failure(400, "flow_clip_range_invalid")
			}
			return clips, nil
		})
	case positional && request.Method == http.MethodDelete:
		return service.editFlowSceneClips(ctx, record, project, id, func(clips []flowSceneClip) ([]flowSceneClip, error) {
			if index >= len(clips) {
				return nil, failure(404, "flow_clip_not_found")
			}
			return slices.Delete(clips, index, index+1), nil
		})
	}
	return httpResponse{}, failure(404, "flow_route_not_found")
}

func (service *service) editFlowSceneClips(ctx context.Context, record storageRecord, project, scene string, edit func([]flowSceneClip) ([]flowSceneClip, error)) (httpResponse, error) {
	clips, err := service.readFlowSceneClips(ctx, record, project, scene)
	if err != nil {
		return httpResponse{}, err
	}
	if clips, err = edit(clips); err != nil {
		return httpResponse{}, err
	}
	written, err := service.writeFlowSceneClips(ctx, record, project, scene, clips)
	if err != nil {
		return httpResponse{}, err
	}
	return flowJSON(http.StatusOK, flowSceneClipList{Clips: written})
}

func (service *service) addFlowSceneClips(ctx context.Context, record storageRecord, project, scene string, contents any, body []byte) (httpResponse, error) {
	var input flowSceneClipAdd
	if err := flowStrict("scene_clip", body, &input); err != nil {
		return httpResponse{}, err
	}
	if len(input.WorkflowIDs) == 0 {
		return httpResponse{}, failure(400, "flow_resource_ids_invalid")
	}
	if err := flowUniqueIdentifiers(input.WorkflowIDs, 50); err != nil {
		return httpResponse{}, err
	}
	if input.Position != nil && *input.Position < 0 ||
		input.StartSeconds != nil && *input.StartSeconds < 0 ||
		input.StartSeconds != nil && input.EndSeconds != nil && *input.EndSeconds <= *input.StartSeconds {
		return httpResponse{}, failure(400, "flow_clip_range_invalid")
	}
	workflows, err := flowWorkflows(contents, project)
	if err != nil {
		return httpResponse{}, err
	}
	for _, id := range input.WorkflowIDs {
		if _, found := flowFindWorkflow(workflows, id); !found {
			return httpResponse{}, failure(404, "flow_workflow_not_found")
		}
	}
	var position, start, end any
	if input.Position != nil {
		position = *input.Position
	}
	if input.StartSeconds != nil {
		start = flowDurationWire(*input.StartSeconds)
	}
	if input.EndSeconds != nil {
		end = flowDurationWire(*input.EndSeconds)
	}
	payload, err := service.flowProjectRPC(ctx, record, project, "oWTRd", flowWireRow(project, scene, input.WorkflowIDs, position, start, end))
	if err != nil {
		return httpResponse{}, err
	}
	return flowJSON(http.StatusCreated, flowSceneClipList{Clips: decodeFlowSceneClips(payload)})
}
