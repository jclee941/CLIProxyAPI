package main

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

type flowAgentEvent struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	CallID    string         `json:"callId,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Result    map[string]any `json:"result,omitempty"`
	Text      string         `json:"text,omitempty"`
	ErrorCode int            `json:"errorCode,omitempty"`
}

type flowAgentMessage struct {
	ID      string           `json:"id"`
	Content map[string]any   `json:"content,omitempty"`
	Events  []flowAgentEvent `json:"events,omitempty"`
}

type flowAgentInputPart struct {
	Text      *string           `json:"text,omitempty"`
	Reference map[string]string `json:"reference,omitempty"`
}

func flowStruct(value any) (map[string]any, error) {
	result := map[string]any{}
	if value == nil {
		return result, nil
	}
	if _, ok := value.([]any); !ok {
		return nil, failure(502, "flow_struct_invalid")
	}
	rows, _ := jsonField(value, 0).([]any)
	for _, row := range rows {
		key, ok := jsonField(row, 0).(string)
		if !ok {
			return nil, failure(502, "flow_struct_invalid")
		}
		decoded, err := flowStructValue(jsonField(row, 1))
		if err != nil {
			return nil, err
		}
		result[key] = decoded
	}
	return result, nil
}

func flowStructValue(value any) (any, error) {
	if value != nil {
		if _, ok := value.([]any); !ok {
			return nil, failure(502, "flow_struct_invalid")
		}
	}
	if value == nil || jsonField(value, 0) != nil {
		return nil, nil
	}
	for _, index := range []int{1, 2} {
		if scalar := jsonField(value, index); scalar != nil {
			return scalar, nil
		}
	}
	if flag := jsonField(value, 3); flag != nil {
		if flag != true && flag != false && flag != float64(0) && flag != float64(1) {
			return nil, failure(502, "flow_struct_invalid")
		}
		return flowFlag(flag), nil
	}
	if object := jsonField(value, 4); object != nil {
		return flowStruct(object)
	}
	result := []any{}
	if list := jsonField(value, 5); list != nil {
		items, _ := jsonField(list, 0).([]any)
		for _, item := range items {
			decoded, err := flowStructValue(item)
			if err != nil {
				return nil, err
			}
			result = append(result, decoded)
		}
		return result, nil
	}
	return nil, nil
}

func decodeFlowAgentMessage(raw any) (flowAgentMessage, error) {
	var message flowAgentMessage
	message.ID, _ = jsonField(raw, 2).(string)
	if message.ID == "" {
		return message, failure(502, "flow_agent_message_invalid")
	}
	var err error
	if value := jsonField(raw, 0); value != nil {
		message.Content, err = flowStruct(value)
		if err != nil {
			return message, err
		}
	}
	rows, _ := jsonField(raw, 1).([]any)
	for _, row := range rows {
		event := flowAgentEvent{}
		event.ID, _ = jsonField(row, 0).(string)
		switch {
		case jsonField(row, 3) != nil:
			event.Type = "tool_call"
			event.CallID, _ = jsonField(row, 3, 0).(string)
			event.Name, _ = jsonField(row, 3, 1).(string)
			event.Arguments, err = flowStruct(jsonField(row, 3, 2))
		case jsonField(row, 4) != nil:
			event.Type = "tool_result"
			event.Name, _ = jsonField(row, 4, 0).(string)
			event.Result, err = flowStruct(jsonField(row, 4, 1))
		case jsonField(row, 6) != nil:
			event.Type = "error"
			event.ErrorCode, _ = jsonInteger(jsonField(row, 6, 0))
		case jsonField(row, 7) != nil:
			event.Type = "title"
			event.Text, _ = jsonField(row, 7, 0).(string)
		default:
			return message, failure(502, "flow_agent_event_unsupported")
		}
		if err != nil {
			return message, err
		}
		message.Events = append(message.Events, event)
	}
	return message, nil
}

func flowAgentPrompt(prompt string, structured json.RawMessage) ([]any, error) {
	input := flowInput{prompt: strings.TrimSpace(prompt)}
	if !utf8.ValidString(prompt) {
		return nil, failure(400, "flow_agent_prompt_invalid")
	}
	if len(structured) != 0 {
		body, err := json.Marshal(map[string]json.RawMessage{"structuredPrompt": structured})
		if err != nil {
			return nil, failure(400, "flow_agent_prompt_invalid")
		}
		if err := parseFlowOptions(flowModel{video: true}, body, &input); err != nil {
			return nil, err
		}
	}
	if input.prompt == "" && len(input.options.StructuredPrompt) == 0 {
		return nil, failure(400, "flow_agent_prompt_invalid")
	}
	return flowPromptWire(input), nil
}
