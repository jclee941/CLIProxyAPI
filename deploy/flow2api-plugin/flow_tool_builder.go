package main

import (
	"context"
	"encoding/json"
	"net/http"
)

type flowToolRunError struct {
	Code         string `json:"code"`
	ProviderCode int    `json:"providerCode"`
}

type flowToolRun struct {
	ToolID    string            `json:"toolId,omitempty"`
	ProjectID string            `json:"projectId"`
	RequestID string            `json:"requestId"`
	Status    string            `json:"status"`
	VersionID string            `json:"versionId,omitempty"`
	Text      string            `json:"text,omitempty"`
	Thought   string            `json:"thought,omitempty"`
	Files     []flowToolFile    `json:"files"`
	Error     *flowToolRunError `json:"error,omitempty"`
}

func (service *service) buildFlowTool(ctx context.Context, record storageRecord, id string, request flowHTTPRequest) (httpResponse, error) {
	var input struct {
		ProjectID        string          `json:"projectId"`
		Prompt           string          `json:"prompt"`
		StructuredPrompt json.RawMessage `json:"structuredPrompt"`
		RequestID        string          `json:"requestId"`
	}
	if err := flowStrict("tool_build", request.Body, &input); err != nil {
		return httpResponse{}, err
	}
	if !flowUUIDPattern.MatchString(input.ProjectID) {
		return httpResponse{}, failure(400, "flow_project_id_invalid")
	}
	prompt, err := flowAgentPrompt(input.Prompt, input.StructuredPrompt)
	if err != nil {
		return httpResponse{}, err
	}
	requestID, err := flowRequestID(input.RequestID)
	if err != nil {
		return httpResponse{}, err
	}
	if _, err := service.getFlowProject(ctx, record, input.ProjectID); err != nil {
		return httpResponse{}, err
	}
	initial := id == ""
	turn := 1
	seed := id
	if initial {
		seed = flowID()
	} else {
		tool, err := service.findOwnedFlowTool(ctx, record, id)
		if err != nil {
			return httpResponse{}, err
		}
		if tool.VersionID != "" {
			version, err := service.readFlowToolVersion(ctx, record, id, tool.VersionID)
			if err != nil {
				return httpResponse{}, err
			}
			for _, message := range version.Messages {
				if message.Role == "user" {
					turn++
				}
			}
		}
	}
	frames, err := service.flowStream(ctx, record, input.ProjectID, requestID, flowAppletStream, func(token string) any {
		return []any{prompt, seed, nil, nil, []any{[]any{token, 1}, turn}, initial}
	})
	if err != nil {
		return httpResponse{}, err
	}
	result := flowToolRun{ToolID: id, ProjectID: input.ProjectID, RequestID: requestID, Status: "complete", Files: []flowToolFile{}}
	positions := map[string]int{}
	status := http.StatusOK
	if initial {
		status = http.StatusCreated
	}
	for _, frame := range frames {
		if toolID, _ := jsonField(frame, 0).(string); toolID != "" {
			if !flowToolIDPattern.MatchString(toolID) || result.ToolID != "" && result.ToolID != toolID {
				return httpResponse{}, failure(502, "flow_tool_identity_mismatch")
			}
			result.ToolID = toolID
		}
		if text, ok := jsonField(frame, 1).(string); ok {
			result.Text += text
		}
		if thought, ok := jsonField(frame, 3).(string); ok {
			result.Thought += thought
		}
		files, err := decodeFlowToolFiles(jsonField(frame, 4))
		if err != nil {
			return httpResponse{}, err
		}
		for _, file := range files {
			if index, found := positions[file.Name]; found {
				result.Files[index] = file
			} else {
				positions[file.Name] = len(result.Files)
				result.Files = append(result.Files, file)
			}
		}
		if version, ok := jsonField(frame, 6).(string); ok && version != "" {
			result.VersionID = version
		}
		if jsonField(frame, 8) != nil {
			code, _ := jsonInteger(jsonField(frame, 8, 0))
			result.Status, result.Error = "failed", &flowToolRunError{Code: "flow_tool_run_failed", ProviderCode: code}
			status = http.StatusUnprocessableEntity
			switch code {
			case 2, 32:
				status = http.StatusTooManyRequests
			case 9:
				status = http.StatusConflict
			case 11:
				status = http.StatusForbidden
			}
		}
	}
	if result.Error == nil && (result.ToolID == "" || result.Text == "" && len(result.Files) == 0) {
		return httpResponse{}, failure(502, "flow_tool_output_missing")
	}
	return flowJSON(status, result)
}
