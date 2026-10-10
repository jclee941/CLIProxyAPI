package main

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var flowToolIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

const flowToolOwnerOwned = "owned"

type flowToolResource struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Description        string `json:"description,omitempty"`
	VersionID          string `json:"versionId,omitempty"`
	CreatedAt          string `json:"createdAt,omitempty"`
	UpdatedAt          string `json:"updatedAt,omitempty"`
	ResourceName       string `json:"resourceName,omitempty"`
	Owner              string `json:"owner"`
	Favorited          bool   `json:"favorited"`
	AllowRemix         bool   `json:"allowRemix"`
	ThumbnailURL       string `json:"thumbnailUrl,omitempty"`
	ThumbnailMediaID   string `json:"thumbnailMediaId,omitempty"`
	StaticThumbnailURL string `json:"staticThumbnailUrl,omitempty"`
}

type flowSharedToolResource struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Description  string `json:"description,omitempty"`
	ResourceName string `json:"resourceName,omitempty"`
	ThumbnailURL string `json:"thumbnailUrl,omitempty"`
}

// decodeFlowTool reads a ListApplets row: id 0, name 2, description 3,
// version 5, created 7, updated 8, resource name 11, owner kind 12
// (0/1 owned, 2 template, 3 community), favorited 14, thumbnail URL 15,
// thumbnail media 16, static thumbnail 19 and disallow-remix 23.
func decodeFlowTool(row any) (flowToolResource, error) {
	id, _ := jsonField(row, 0).(string)
	if !flowToolIDPattern.MatchString(id) {
		return flowToolResource{}, failure(502, "flow_tool_response_invalid")
	}
	tool := flowToolResource{ID: id, Owner: "unknown", AllowRemix: !flowFlag(jsonField(row, 23))}
	tool.Name, _ = jsonField(row, 2).(string)
	tool.Description, _ = jsonField(row, 3).(string)
	tool.VersionID, _ = jsonField(row, 5).(string)
	tool.CreatedAt = flowTimestamp(jsonField(row, 7))
	tool.UpdatedAt = flowTimestamp(jsonField(row, 8))
	tool.ResourceName, _ = jsonField(row, 11).(string)
	tool.Favorited = flowFlag(jsonField(row, 14))
	tool.ThumbnailURL, _ = jsonField(row, 15).(string)
	tool.ThumbnailMediaID, _ = jsonField(row, 16).(string)
	tool.StaticThumbnailURL, _ = jsonField(row, 19).(string)
	code, valid := 0, true
	if raw := jsonField(row, 12); raw != nil {
		code, valid = jsonInteger(raw)
	}
	switch {
	case !valid:
	case code == 0 || code == 1:
		tool.Owner = flowToolOwnerOwned
	case code == 2:
		tool.Owner = "template"
	case code == 3:
		tool.Owner = "community"
	}
	return tool, nil
}

func (service *service) listFlowTools(ctx context.Context, record storageRecord) ([]flowToolResource, error) {
	payload, err := service.flowAccountRPC(ctx, record, "tRARke", []any{})
	if err != nil {
		return nil, err
	}
	rows, ok := jsonField(payload, 0).([]any)
	if _, valid := payload.([]any); !valid || !ok && jsonField(payload, 0) != nil {
		return nil, failure(502, "flow_tool_list_invalid")
	}
	tools := []flowToolResource{}
	for _, row := range rows {
		tool, err := decodeFlowTool(row)
		if err != nil {
			return nil, err
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// findFlowTool resolves a tool through the account list; the provider has no
// proven single-read payload, and the list is also the ownership authority.
func (service *service) findFlowTool(ctx context.Context, record storageRecord, id string) (flowToolResource, error) {
	tools, err := service.listFlowTools(ctx, record)
	if err != nil {
		return flowToolResource{}, err
	}
	for _, tool := range tools {
		if tool.ID == id {
			return tool, nil
		}
	}
	return flowToolResource{}, failure(404, "flow_tool_not_found")
}

func (service *service) findOwnedFlowTool(ctx context.Context, record storageRecord, id string) (flowToolResource, error) {
	tool, err := service.findFlowTool(ctx, record, id)
	if err == nil && tool.Owner != flowToolOwnerOwned {
		return flowToolResource{}, failure(403, "flow_tool_not_owned")
	}
	return tool, err
}

func flowToolNoContent() httpResponse {
	return httpResponse{StatusCode: http.StatusNoContent, Headers: http.Header{"Cache-Control": {"private, no-store"}}}
}

func flowToolEmptyBody(key string, body []byte) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	var empty struct{}
	return flowStrict(key, body, &empty)
}

func flowToolText(value string, limit int, allowEmpty, multiline bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit {
		return false
	}
	if value == "" {
		return allowEmpty
	}
	for _, character := range value {
		if unicode.IsControl(character) && !(multiline && (character == '\n' || character == '\r' || character == '\t')) {
			return false
		}
	}
	return true
}

func flowToolHTTPSURL(value string) bool {
	parsed, err := url.Parse(value)
	return len(value) <= 2048 && err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

// flowToolHTTP serves the account-wide Flow tools. tail has no leading slash:
// "", "{id}", "{id}:action", "{id}/versions", "{id}/versions/{version}" and
// "{id}/versions/{version}:restore".
func (service *service) flowToolHTTP(ctx context.Context, record storageRecord, tail string, request flowHTTPRequest) (httpResponse, error) {
	if tail == "" {
		if request.Method != http.MethodGet {
			return httpResponse{}, failure(404, "flow_route_not_found")
		}
		tools, err := service.listFlowTools(ctx, record)
		if err != nil {
			return httpResponse{}, err
		}
		return flowJSON(http.StatusOK, map[string]any{"tools": tools})
	}
	parts := strings.Split(tail, "/")
	id, action, _ := strings.Cut(parts[0], ":")
	if !flowToolIDPattern.MatchString(id) {
		return httpResponse{}, failure(400, "flow_tool_id_invalid")
	}
	switch {
	case len(parts) == 1:
		return service.flowToolItemHTTP(ctx, record, id, action, request)
	case len(parts) == 2 && parts[1] == "versions" && action == "":
		return service.flowToolVersionsHTTP(ctx, record, id, request)
	case len(parts) == 3 && parts[1] == "versions" && action == "":
		return service.flowToolVersionHTTP(ctx, record, id, parts[2], request)
	}
	return httpResponse{}, failure(404, "flow_route_not_found")
}

func (service *service) flowToolItemHTTP(ctx context.Context, record storageRecord, id, action string, request flowHTTPRequest) (httpResponse, error) {
	switch {
	case action == "" && request.Method == http.MethodGet:
		tool, err := service.findFlowTool(ctx, record, id)
		if err != nil {
			return httpResponse{}, err
		}
		return flowJSON(http.StatusOK, tool)
	case action == "" && request.Method == http.MethodPatch:
		return service.updateFlowTool(ctx, record, id, request.Body)
	case action == "" && request.Method == http.MethodDelete:
		if _, err := service.findOwnedFlowTool(ctx, record, id); err != nil {
			return httpResponse{}, err
		}
		if _, err := service.flowAccountRPC(ctx, record, "zpwzEe", []any{"applets/" + id}); err != nil {
			return httpResponse{}, err
		}
		return flowToolNoContent(), nil
	case request.Method != http.MethodPost:
	case action == "copy":
		return service.copyFlowTool(ctx, record, id, request.Body)
	case action == "favorite" || action == "unfavorite":
		return service.favoriteFlowTool(ctx, record, id, action == "favorite", request.Body)
	case action == "share":
		return service.shareFlowTool(ctx, record, id, request.Body)
	}
	return httpResponse{}, failure(404, "flow_route_not_found")
}

func (service *service) updateFlowTool(ctx context.Context, record storageRecord, id string, body []byte) (httpResponse, error) {
	var input struct {
		DisplayName        *string `json:"displayName"`
		Description        *string `json:"description"`
		ThumbnailMediaID   *string `json:"thumbnailMediaId"`
		StaticThumbnailURL *string `json:"staticThumbnailUrl"`
	}
	if err := flowStrict("tool_update", body, &input); err != nil {
		return httpResponse{}, err
	}
	row := make([]any, 12)
	masks := []any{}
	set := func(index int, mask, value string) {
		for len(row) <= index {
			row = append(row, nil)
		}
		row[index] = value
		masks = append(masks, mask)
	}
	if input.DisplayName != nil {
		name := strings.TrimSpace(*input.DisplayName)
		if !flowToolText(name, 256, false, false) {
			return httpResponse{}, failure(400, "flow_tool_name_invalid")
		}
		set(2, "display_name", name)
	}
	if input.Description != nil {
		if !flowToolText(*input.Description, 4000, true, true) {
			return httpResponse{}, failure(400, "flow_tool_description_invalid")
		}
		set(3, "description", *input.Description)
	}
	if input.ThumbnailMediaID != nil {
		if *input.ThumbnailMediaID != "" && !flowIdentifier(*input.ThumbnailMediaID) {
			return httpResponse{}, failure(400, "flow_tool_thumbnail_invalid")
		}
		set(16, "thumbnail_media_id", *input.ThumbnailMediaID)
	}
	if input.StaticThumbnailURL != nil {
		if *input.StaticThumbnailURL != "" && !flowToolHTTPSURL(*input.StaticThumbnailURL) {
			return httpResponse{}, failure(400, "flow_tool_thumbnail_invalid")
		}
		set(19, "static_thumbnail_url", *input.StaticThumbnailURL)
	}
	if len(masks) == 0 {
		return httpResponse{}, failure(400, "flow_tool_update_empty")
	}
	tool, err := service.findOwnedFlowTool(ctx, record, id)
	if err != nil {
		return httpResponse{}, err
	}
	if tool.ResourceName == "" {
		return httpResponse{}, failure(502, "flow_tool_resource_name_missing")
	}
	row[11] = tool.ResourceName
	if _, err := service.flowAccountRPC(ctx, record, "sd0GXe", []any{row, []any{masks}}); err != nil {
		return httpResponse{}, err
	}
	updated, err := service.findFlowTool(ctx, record, id)
	if err != nil {
		return httpResponse{}, err
	}
	return flowJSON(http.StatusOK, updated)
}

// copyFlowTool copies any visible tool, templates included. The reply shape is
// not proven, so an unrecognised reply still reports the committed copy.
func (service *service) copyFlowTool(ctx context.Context, record storageRecord, id string, body []byte) (httpResponse, error) {
	if err := flowToolEmptyBody("tool_copy", body); err != nil {
		return httpResponse{}, err
	}
	if _, err := service.findFlowTool(ctx, record, id); err != nil {
		return httpResponse{}, err
	}
	payload, err := service.flowAccountRPC(ctx, record, "VVrfbf", []any{id})
	if err != nil {
		return httpResponse{}, err
	}
	candidate := payload
	if _, wrapped := jsonField(payload, 0).([]any); wrapped {
		candidate = jsonField(payload, 0)
	}
	var copied *flowToolResource
	if tool, err := decodeFlowTool(candidate); err == nil {
		copied = &tool
	}
	return flowJSON(http.StatusCreated, map[string]any{"sourceId": id, "tool": copied})
}

func (service *service) favoriteFlowTool(ctx context.Context, record storageRecord, id string, favorite bool, body []byte) (httpResponse, error) {
	if err := flowToolEmptyBody("tool_favorite", body); err != nil {
		return httpResponse{}, err
	}
	if _, err := service.findFlowTool(ctx, record, id); err != nil {
		return httpResponse{}, err
	}
	rpc := "TsaPHc"
	if favorite {
		rpc = "eDee9c"
	}
	if _, err := service.flowAccountRPC(ctx, record, rpc, []any{id}); err != nil {
		return httpResponse{}, err
	}
	return flowJSON(http.StatusOK, map[string]any{"id": id, "favorited": favorite})
}

func (service *service) shareFlowTool(ctx context.Context, record storageRecord, id string, body []byte) (httpResponse, error) {
	var input struct {
		AllowRemix *bool `json:"allowRemix"`
	}
	if err := flowStrict("tool_share", body, &input); err != nil {
		return httpResponse{}, err
	}
	if input.AllowRemix == nil {
		return httpResponse{}, failure(400, "flow_tool_share_allow_remix_required")
	}
	tool, err := service.findOwnedFlowTool(ctx, record, id)
	if err != nil {
		return httpResponse{}, err
	}
	if tool.VersionID == "" {
		return httpResponse{}, failure(409, "flow_tool_version_missing")
	}
	payload, err := service.flowAccountRPC(ctx, record, "vEIlvc", []any{id, tool.VersionID, !*input.AllowRemix})
	if err != nil {
		return httpResponse{}, err
	}
	sharedID, _ := jsonField(payload, 0).(string)
	if !flowToolIDPattern.MatchString(sharedID) {
		return httpResponse{}, failure(502, "flow_tool_share_response_invalid")
	}
	return flowJSON(http.StatusCreated, map[string]any{
		"sharedId": sharedID, "toolId": id, "versionId": tool.VersionID, "allowRemix": *input.AllowRemix,
		"url": "https://labs.google/fx/tools/flow/shared/tool/" + url.PathEscape(sharedID),
	})
}

// decodeFlowSavedSharedTool reads a ListSavedSharedApplets row from the proto
// getter fields shared id 1, title 2, description 3 and thumbnail 5.
func decodeFlowSavedSharedTool(row any) (flowSharedToolResource, error) {
	id, _ := jsonField(row, 0).(string)
	if !flowToolIDPattern.MatchString(id) {
		return flowSharedToolResource{}, failure(502, "flow_shared_tool_response_invalid")
	}
	shared := flowSharedToolResource{ID: id}
	shared.Title, _ = jsonField(row, 1).(string)
	shared.Description, _ = jsonField(row, 2).(string)
	shared.ThumbnailURL, _ = jsonField(row, 4).(string)
	return shared, nil
}

func (service *service) listSavedFlowSharedTools(ctx context.Context, record storageRecord) ([]flowSharedToolResource, error) {
	payload, err := service.flowAccountRPC(ctx, record, "qJcgMc", []any{})
	if err != nil {
		return nil, err
	}
	rows, ok := jsonField(payload, 0).([]any)
	if _, valid := payload.([]any); !valid || !ok && jsonField(payload, 0) != nil {
		return nil, failure(502, "flow_shared_tool_list_invalid")
	}
	shared := []flowSharedToolResource{}
	for _, row := range rows {
		item, err := decodeFlowSavedSharedTool(row)
		if err != nil {
			return nil, err
		}
		shared = append(shared, item)
	}
	return shared, nil
}

// flowSharedToolHTTP serves shared (published) tools. tail has no leading
// slash: "", "{id}" and "{id}:fork|favorite|unfavorite". Unshare is not
// exposed because the account has no proven list of its own shared links.
func (service *service) flowSharedToolHTTP(ctx context.Context, record storageRecord, tail string, request flowHTTPRequest) (httpResponse, error) {
	if tail == "" {
		if request.Method != http.MethodGet {
			return httpResponse{}, failure(404, "flow_route_not_found")
		}
		shared, err := service.listSavedFlowSharedTools(ctx, record)
		if err != nil {
			return httpResponse{}, err
		}
		return flowJSON(http.StatusOK, map[string]any{"sharedTools": shared})
	}
	id, action, _ := strings.Cut(tail, ":")
	if strings.Contains(id, "/") {
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	if !flowToolIDPattern.MatchString(id) {
		return httpResponse{}, failure(400, "flow_shared_tool_id_invalid")
	}
	switch {
	case action == "" && request.Method == http.MethodGet:
		return service.getFlowSharedTool(ctx, record, id)
	case action == "" && request.Method == http.MethodDelete:
		saved, err := service.listSavedFlowSharedTools(ctx, record)
		if err != nil {
			return httpResponse{}, err
		}
		found := false
		for _, item := range saved {
			found = found || item.ID == id
		}
		if !found {
			return httpResponse{}, failure(404, "flow_shared_tool_not_saved")
		}
		if _, err := service.flowAccountRPC(ctx, record, "ipGN1", []any{id}); err != nil {
			return httpResponse{}, err
		}
		return flowToolNoContent(), nil
	case request.Method != http.MethodPost:
	case action == "fork":
		return service.forkFlowSharedTool(ctx, record, id, request.Body)
	case action == "favorite" || action == "unfavorite":
		if err := flowToolEmptyBody("shared_tool_favorite", request.Body); err != nil {
			return httpResponse{}, err
		}
		rpc := "TsaPHc"
		if action == "favorite" {
			rpc = "eDee9c"
		}
		if _, err := service.flowAccountRPC(ctx, record, rpc, []any{nil, id}); err != nil {
			return httpResponse{}, err
		}
		return flowJSON(http.StatusOK, map[string]any{"id": id, "favorited": action == "favorite"})
	}
	return httpResponse{}, failure(404, "flow_route_not_found")
}

// getFlowSharedTool reads [meta, text, files] with meta getter fields shared
// id 1, title 2, description 3, resource name 6 and thumbnail 7.
func (service *service) getFlowSharedTool(ctx context.Context, record storageRecord, id string) (httpResponse, error) {
	payload, err := service.flowAccountRPC(ctx, record, "J0KDW", []any{"sharedApplets/" + id})
	if err != nil {
		return httpResponse{}, err
	}
	if sharedID, _ := jsonField(payload, 0, 0).(string); sharedID != id {
		return httpResponse{}, failure(502, "flow_shared_tool_identity_mismatch")
	}
	detail := struct {
		flowSharedToolResource
		Files []flowToolFile `json:"files"`
	}{flowSharedToolResource: flowSharedToolResource{ID: id}}
	detail.Title, _ = jsonField(payload, 0, 1).(string)
	detail.Description, _ = jsonField(payload, 0, 2).(string)
	detail.ResourceName, _ = jsonField(payload, 0, 5).(string)
	detail.ThumbnailURL, _ = jsonField(payload, 0, 6).(string)
	detail.Files, err = decodeFlowToolFiles(jsonField(payload, 2))
	if err != nil {
		return httpResponse{}, err
	}
	return flowJSON(http.StatusOK, detail)
}

func (service *service) forkFlowSharedTool(ctx context.Context, record storageRecord, id string, body []byte) (httpResponse, error) {
	var input struct {
		ProjectID string `json:"projectId"`
	}
	if err := flowStrict("shared_tool_fork", body, &input); err != nil {
		return httpResponse{}, err
	}
	if !flowUUIDPattern.MatchString(input.ProjectID) {
		return httpResponse{}, failure(400, "flow_project_id_invalid")
	}
	payload, err := service.flowAccountRPC(ctx, record, "CSvxid", []any{id, input.ProjectID})
	if err != nil {
		return httpResponse{}, err
	}
	tool, err := decodeFlowTool(jsonField(payload, 0))
	if err != nil {
		return httpResponse{}, err
	}
	return flowJSON(http.StatusCreated, tool)
}
