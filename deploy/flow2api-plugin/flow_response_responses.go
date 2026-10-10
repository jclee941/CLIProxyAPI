package main

import (
	"fmt"
	"strings"
)

type flowOutText struct {
	Type        string   `json:"type"`
	Text        string   `json:"text"`
	Annotations []string `json:"annotations"`
}
type flowOutMessageItem struct {
	ID      string        `json:"id"`
	Type    string        `json:"type"`
	Status  string        `json:"status"`
	Role    string        `json:"role"`
	Content []flowOutText `json:"content"`
}
type flowOutImageItem struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Status       string `json:"status"`
	Result       string `json:"result,omitempty"`
	OutputFormat string `json:"output_format"`
}
type flowOutResponse struct {
	ID        string                  `json:"id"`
	Object    string                  `json:"object"`
	CreatedAt int64                   `json:"created_at"`
	Status    string                  `json:"status"`
	Model     string                  `json:"model"`
	Output    []any                   `json:"output"`
	Flow      []flowOutMediaReference `json:"flow,omitempty"`
}
type flowOutEvent struct {
	Type         string           `json:"type"`
	Sequence     int              `json:"sequence_number"`
	Response     *flowOutResponse `json:"response,omitempty"`
	Item         any              `json:"item,omitempty"`
	ItemID       string           `json:"item_id,omitempty"`
	OutputIndex  *int             `json:"output_index,omitempty"`
	ContentIndex *int             `json:"content_index,omitempty"`
	Part         *flowOutText     `json:"part,omitempty"`
	Delta        string           `json:"delta,omitempty"`
	Text         string           `json:"text,omitempty"`
}

func flowOutImageFormat(mime string) string {
	return strings.TrimPrefix(mime, "image/")
}

func flowOutItems(reply flowOutReply) []any {
	var items []any
	for _, candidate := range reply.candidates {
		var message *flowOutMessageItem
		flush := func() {
			if message != nil {
				message.ID = fmt.Sprintf("msg_flow_%s_%d", reply.id, len(items))
				items = append(items, *message)
				message = nil
			}
		}
		for _, part := range candidate.parts {
			if part.isImage() {
				flush()
				items = append(items, flowOutImageItem{fmt.Sprintf("ig_flow_%s_%d", reply.id, len(items)), "image_generation_call", "completed", part.data, flowOutImageFormat(part.mime)})
				continue
			}
			if message == nil {
				message = &flowOutMessageItem{Type: "message", Status: "completed", Role: "assistant", Content: []flowOutText{}}
			}
			message.Content = append(message.Content, flowOutText{"output_text", part.content(), []string{}})
		}
		flush()
	}
	return items
}

func flowOutResponses(reply flowOutReply, stream bool) *flowOutFrames {
	frames := &flowOutFrames{}
	items := flowOutItems(reply)
	response := func(status string, output []any) *flowOutResponse {
		return &flowOutResponse{"resp_flow_" + reply.id, "response", reply.created, status, reply.model, output, reply.media}
	}
	if !stream {
		frames.add("", response("completed", items))
		return frames
	}
	sequence := 0
	emit := func(event flowOutEvent) {
		event.Sequence = sequence
		sequence++
		frames.add(event.Type, event)
	}
	emit(flowOutEvent{Type: "response.created", Response: response("in_progress", []any{})})
	emit(flowOutEvent{Type: "response.in_progress", Response: response("in_progress", []any{})})
	for index, item := range items {
		switch item := item.(type) {
		case flowOutImageItem:
			pending := item
			pending.Status, pending.Result = "in_progress", ""
			emit(flowOutEvent{Type: "response.output_item.added", OutputIndex: ptr(index), Item: pending})
			emit(flowOutEvent{Type: "response.image_generation_call.completed", OutputIndex: ptr(index), ItemID: item.ID})
			emit(flowOutEvent{Type: "response.output_item.done", OutputIndex: ptr(index), Item: item})
		case flowOutMessageItem:
			pending := item
			pending.Status, pending.Content = "in_progress", []flowOutText{}
			emit(flowOutEvent{Type: "response.output_item.added", OutputIndex: ptr(index), Item: pending})
			for content, part := range item.Content {
				empty := part
				empty.Text = ""
				emit(flowOutEvent{Type: "response.content_part.added", OutputIndex: ptr(index), ItemID: item.ID, ContentIndex: ptr(content), Part: &empty})
				emit(flowOutEvent{Type: "response.output_text.delta", OutputIndex: ptr(index), ItemID: item.ID, ContentIndex: ptr(content), Delta: part.Text})
				emit(flowOutEvent{Type: "response.output_text.done", OutputIndex: ptr(index), ItemID: item.ID, ContentIndex: ptr(content), Text: part.Text})
				done := part
				emit(flowOutEvent{Type: "response.content_part.done", OutputIndex: ptr(index), ItemID: item.ID, ContentIndex: ptr(content), Part: &done})
			}
			emit(flowOutEvent{Type: "response.output_item.done", OutputIndex: ptr(index), Item: item})
		}
	}
	emit(flowOutEvent{Type: "response.completed", Response: response("completed", items)})
	return frames
}
