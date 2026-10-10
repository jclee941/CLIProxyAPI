package main

import (
	"context"
	"net/http"
	"regexp"
	"strings"
)

// Version markers are public state messages such as
// "[VERSION_SNAPSHOT] version=<uuid>" inside the tool conversation history.
var flowVersionMarkerPattern = regexp.MustCompile(`(?m)^\[VERSION_SNAPSHOT\].*?\bversion=([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`)

type flowToolFile struct {
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Content  string `json:"content"`
}

type flowToolMessage struct {
	ID      string `json:"id,omitempty"`
	Role    string `json:"role,omitempty"`
	Text    string `json:"text"`
	Thought string `json:"thought,omitempty"`
}

type flowToolVersion struct {
	ToolID       string            `json:"toolId"`
	VersionID    string            `json:"versionId"`
	Description  string            `json:"description,omitempty"`
	CreatedAt    string            `json:"createdAt,omitempty"`
	ResourceName string            `json:"resourceName,omitempty"`
	Changes      string            `json:"changes,omitempty"`
	Messages     []flowToolMessage `json:"messages"`
	Files        []flowToolFile    `json:"files"`
}

type flowToolVersionRef struct {
	ID      string `json:"id"`
	Current bool   `json:"current"`
}

// decodeFlowToolFiles reads [name, mimeType, plain-text content] triples and
// keeps every file's content verbatim.
func decodeFlowToolFiles(value any) ([]flowToolFile, error) {
	files := []flowToolFile{}
	rows, ok := value.([]any)
	if !ok && value != nil {
		return nil, failure(502, "flow_tool_files_invalid")
	}
	for _, row := range rows {
		name, _ := jsonField(row, 0).(string)
		mimeType, _ := jsonField(row, 1).(string)
		content, isText := jsonField(row, 2).(string)
		if name == "" || !isText && jsonField(row, 2) != nil {
			return nil, failure(502, "flow_tool_files_invalid")
		}
		files = append(files, flowToolFile{Name: name, MimeType: mimeType, Content: content})
	}
	return files, nil
}

// readFlowToolVersion reads [meta, changes, [session, messages], files] with
// meta = tool 0, version 1, description 4, created 7, resource name 8 and
// message = text 0, role 1, thought 6, id 9.
func (service *service) readFlowToolVersion(ctx context.Context, record storageRecord, toolID, versionID string) (flowToolVersion, error) {
	payload, err := service.flowAccountRPC(ctx, record, "ZLaYcd", []any{"applets/" + toolID + "/versions/" + versionID})
	if err != nil {
		return flowToolVersion{}, err
	}
	if tool, _ := jsonField(payload, 0, 0).(string); tool != toolID {
		return flowToolVersion{}, failure(502, "flow_tool_version_identity_mismatch")
	}
	if version, _ := jsonField(payload, 0, 1).(string); version != versionID {
		return flowToolVersion{}, failure(502, "flow_tool_version_identity_mismatch")
	}
	result := flowToolVersion{ToolID: toolID, VersionID: versionID, Messages: []flowToolMessage{}}
	result.Description, _ = jsonField(payload, 0, 4).(string)
	result.CreatedAt = flowTimestamp(jsonField(payload, 0, 7))
	result.ResourceName, _ = jsonField(payload, 0, 8).(string)
	result.Changes, _ = jsonField(payload, 1).(string)
	messages, ok := jsonField(payload, 2, 1).([]any)
	if !ok && jsonField(payload, 2, 1) != nil {
		return flowToolVersion{}, failure(502, "flow_tool_history_invalid")
	}
	for _, message := range messages {
		item := flowToolMessage{}
		item.Role, _ = jsonField(message, 1).(string)
		item.Thought, _ = jsonField(message, 6).(string)
		item.Text, _ = jsonField(message, 0).(string)
		item.ID, _ = jsonField(message, 9).(string)
		result.Messages = append(result.Messages, item)
	}
	result.Files, err = decodeFlowToolFiles(jsonField(payload, 3))
	return result, err
}

// flowToolVersionsHTTP lists versions without a provider list RPC: the ids
// are the snapshot markers in the current version's history plus the current
// version itself.
func (service *service) flowToolVersionsHTTP(ctx context.Context, record storageRecord, id string, request flowHTTPRequest) (httpResponse, error) {
	if request.Method != http.MethodGet {
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	tool, err := service.findFlowTool(ctx, record, id)
	if err != nil {
		return httpResponse{}, err
	}
	versions := []flowToolVersionRef{}
	if tool.VersionID != "" {
		current, err := service.readFlowToolVersion(ctx, record, id, tool.VersionID)
		if err != nil {
			return httpResponse{}, err
		}
		seen := map[string]bool{tool.VersionID: true}
		for _, message := range current.Messages {
			match := flowVersionMarkerPattern.FindStringSubmatch(message.Text)
			if match == nil || seen[match[1]] {
				continue
			}
			seen[match[1]] = true
			versions = append(versions, flowToolVersionRef{ID: match[1]})
		}
		versions = append(versions, flowToolVersionRef{ID: tool.VersionID, Current: true})
	}
	return flowJSON(http.StatusOK, map[string]any{"toolId": id, "versions": versions})
}

func (service *service) flowToolVersionHTTP(ctx context.Context, record storageRecord, id, versionPart string, request flowHTTPRequest) (httpResponse, error) {
	version, action, _ := strings.Cut(versionPart, ":")
	if !flowToolIDPattern.MatchString(version) {
		return httpResponse{}, failure(400, "flow_tool_version_id_invalid")
	}
	switch {
	case action == "" && request.Method == http.MethodGet:
		if _, err := service.findFlowTool(ctx, record, id); err != nil {
			return httpResponse{}, err
		}
		result, err := service.readFlowToolVersion(ctx, record, id, version)
		if err != nil {
			return httpResponse{}, err
		}
		return flowJSON(http.StatusOK, result)
	case action == "restore" && request.Method == http.MethodPost:
		if err := flowToolEmptyBody("tool_version_restore", request.Body); err != nil {
			return httpResponse{}, err
		}
		if _, err := service.findOwnedFlowTool(ctx, record, id); err != nil {
			return httpResponse{}, err
		}
		if _, err := service.flowAccountRPC(ctx, record, "CvpHmb", []any{id, version}); err != nil {
			return httpResponse{}, err
		}
		tool, err := service.findFlowTool(ctx, record, id)
		if err != nil {
			return httpResponse{}, err
		}
		return flowJSON(http.StatusOK, map[string]any{"restoredVersionId": version, "tool": tool})
	}
	return httpResponse{}, failure(404, "flow_route_not_found")
}
