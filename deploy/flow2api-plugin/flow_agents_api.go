package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type flowAgentSession struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Title     string `json:"title"`
	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type flowAgentTurn struct {
	Parts    []flowAgentInputPart `json:"parts"`
	Messages []flowAgentMessage   `json:"messages"`
	Input    string               `json:"input,omitempty"`
}

type flowAgentChatResult struct {
	SessionID string             `json:"sessionId"`
	RequestID string             `json:"requestId"`
	Messages  []flowAgentMessage `json:"messages"`
}

func decodeFlowAgentSession(raw any, project string) (flowAgentSession, error) {
	id, _ := jsonField(raw, 0).(string)
	title, _ := jsonField(raw, 1, 0).(string)
	if !flowUUIDPattern.MatchString(id) {
		return flowAgentSession{}, failure(502, "flow_agent_session_invalid")
	}
	return flowAgentSession{ID: id, ProjectID: project, Title: title,
		CreatedAt: flowTimestamp(jsonField(raw, 1, 1)), UpdatedAt: flowTimestamp(jsonField(raw, 1, 2))}, nil
}

func (service *service) flowAgentSessions(ctx context.Context, record storageRecord, project string) ([]flowAgentSession, error) {
	payload, err := service.flowProjectRPC(ctx, record, project, "mrlkwd", []any{project})
	if err != nil {
		return nil, err
	}
	result := []flowAgentSession{}
	for _, row := range flowContentRows(payload, 0) {
		session, err := decodeFlowAgentSession(row, project)
		if err != nil {
			return nil, err
		}
		result = append(result, session)
	}
	return result, nil
}

func (service *service) flowAgentHTTP(ctx context.Context, record storageRecord, project, tail string, request flowHTTPRequest) (httpResponse, error) {
	if tail == "" {
		switch request.Method {
		case http.MethodGet:
			sessions, err := service.flowAgentSessions(ctx, record, project)
			if err != nil {
				return httpResponse{}, err
			}
			return flowJSON(200, map[string]any{"sessions": sessions})
		case http.MethodPost:
			if len(request.Body) != 0 {
				var empty struct{}
				if err := flowStrict("session_create", request.Body, &empty); err != nil {
					return httpResponse{}, err
				}
			}
			payload, err := service.flowProjectRPC(ctx, record, project, "csbIsb", []any{project, nil, flowID()})
			if err != nil {
				return httpResponse{}, err
			}
			session, err := decodeFlowAgentSession(jsonField(payload, 0), project)
			if err != nil {
				return httpResponse{}, err
			}
			return flowJSON(201, session)
		}
	}
	id, action, _ := strings.Cut(tail, ":")
	if !flowUUIDPattern.MatchString(id) {
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	sessions, err := service.flowAgentSessions(ctx, record, project)
	if err != nil {
		return httpResponse{}, err
	}
	var current *flowAgentSession
	for index := range sessions {
		if sessions[index].ID == id {
			current = &sessions[index]
			break
		}
	}
	if current == nil {
		return httpResponse{}, failure(404, "flow_agent_session_not_found")
	}
	switch {
	case request.Method == http.MethodGet && action == "":
		return service.getFlowAgentHistory(ctx, record, project, id)
	case request.Method == http.MethodPatch && action == "":
		title, err := flowProjectTitle(request.Body)
		if err != nil {
			return httpResponse{}, err
		}
		if _, err := service.flowProjectRPC(ctx, record, project, "TunYMc", []any{id, title, project}); err != nil {
			return httpResponse{}, err
		}
		current.Title = title
		return flowJSON(200, current)
	case request.Method == http.MethodDelete && action == "":
		if _, err := service.flowProjectRPC(ctx, record, project, "Dcn2Le", []any{id}); err != nil {
			return httpResponse{}, err
		}
		return flowNoContent(), nil
	case request.Method == http.MethodPost && action == "chat":
		return service.chatFlowAgent(ctx, record, project, id, request)
	case request.Method == http.MethodPost && action == "cancel":
		return service.cancelFlowRequest(ctx, record, "agent_"+id+":cancel", request)
	}
	return httpResponse{}, failure(404, "flow_route_not_found")
}

func (service *service) getFlowAgentHistory(ctx context.Context, record storageRecord, project, id string) (httpResponse, error) {
	payload, err := service.flowProjectRPC(ctx, record, project, "GN0Bre", []any{id})
	if err != nil {
		return httpResponse{}, err
	}
	session, err := decodeFlowAgentSession(jsonField(payload, 0), project)
	if err != nil || session.ID != id {
		return httpResponse{}, failure(502, "flow_agent_session_invalid")
	}
	turns := []flowAgentTurn{}
	for _, raw := range flowContentRows(payload, 1) {
		turn := flowAgentTurn{Messages: []flowAgentMessage{}, Parts: []flowAgentInputPart{}}
		parts, _ := jsonField(raw, 0, 0, 0).([]any)
		for _, part := range parts {
			if text, ok := jsonField(part, 0).(string); ok {
				turn.Input += text
				turn.Parts = append(turn.Parts, flowAgentInputPart{Text: &text})
			} else {
				reference := map[string]string{}
				for index, key := range []string{"mediaId", "audioId", "entityId", "likenessId"} {
					if id, ok := jsonField(part, 1, index, 0).(string); ok {
						reference[key] = id
					}
				}
				if len(reference) != 0 {
					turn.Parts = append(turn.Parts, flowAgentInputPart{Reference: reference})
				}
			}
		}
		for _, row := range flowContentRows(raw, 1) {
			message, err := decodeFlowAgentMessage(row)
			if err != nil {
				return httpResponse{}, err
			}
			turn.Messages = append(turn.Messages, message)
		}
		turns = append(turns, turn)
	}
	return flowJSON(200, struct {
		flowAgentSession
		Turns []flowAgentTurn `json:"turns"`
	}{session, turns})
}

func (service *service) chatFlowAgent(ctx context.Context, record storageRecord, project, id string, request flowHTTPRequest) (httpResponse, error) {
	var input struct {
		Prompt           string          `json:"prompt"`
		StructuredPrompt json.RawMessage `json:"structuredPrompt"`
		CollectionID     string          `json:"collectionId"`
	}
	if err := flowStrict("agent_chat", request.Body, &input); err != nil {
		return httpResponse{}, err
	}
	prompt, err := flowAgentPrompt(input.Prompt, input.StructuredPrompt)
	if err != nil {
		return httpResponse{}, err
	}
	var collection any
	if input.CollectionID != "" {
		contents, err := service.flowProjectContents(ctx, record, project)
		if err != nil {
			return httpResponse{}, err
		}
		if collection, err = flowDestinationCollection(contents, project, input.CollectionID); err != nil {
			return httpResponse{}, err
		}
	}
	requestID := "agent_" + id
	frames, err := service.flowStream(ctx, record, project, requestID, flowCreationStream, func(token string) any {
		return []any{id, []any{prompt}, []any{"projects/" + project, nil, []any{token, 1}, nil, collection, 1}}
	})
	if err != nil {
		return httpResponse{}, err
	}
	result := flowAgentChatResult{SessionID: id, RequestID: requestID, Messages: []flowAgentMessage{}}
	positions := map[string]int{}
	status := http.StatusOK
	for _, frame := range frames {
		if flowFlag(jsonField(frame, 2)) || jsonField(frame, 0) == nil {
			continue
		}
		message, err := decodeFlowAgentMessage(jsonField(frame, 0))
		if err != nil {
			return httpResponse{}, err
		}
		for _, event := range message.Events {
			if event.Type == "error" {
				status = http.StatusUnprocessableEntity
			}
		}
		if position, found := positions[message.ID]; found {
			result.Messages[position] = message
		} else {
			positions[message.ID] = len(result.Messages)
			result.Messages = append(result.Messages, message)
		}
	}
	if len(result.Messages) == 0 {
		return httpResponse{}, failure(502, "flow_agent_output_missing")
	}
	return flowJSON(status, result)
}
