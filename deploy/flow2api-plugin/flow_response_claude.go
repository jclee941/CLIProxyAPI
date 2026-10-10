package main

// Claude has no assistant image block, so media travels as text blocks holding
// data URLs. The schema requires a usage object; it is zeros like the core
// translator, never an estimate.
type flowOutClaudeBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
type flowOutClaudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
type flowOutClaudeMessage struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Model        string                  `json:"model"`
	Content      []flowOutClaudeBlock    `json:"content"`
	StopReason   *string                 `json:"stop_reason"`
	StopSequence *string                 `json:"stop_sequence"`
	Usage        flowOutClaudeUsage      `json:"usage"`
	Flow         []flowOutMediaReference `json:"flow,omitempty"`
}
type flowOutClaudeDelta struct {
	Type       string  `json:"type,omitempty"`
	Text       string  `json:"text,omitempty"`
	StopReason *string `json:"stop_reason,omitempty"`
}
type flowOutClaudeEvent struct {
	Type         string                `json:"type"`
	Message      *flowOutClaudeMessage `json:"message,omitempty"`
	Index        *int                  `json:"index,omitempty"`
	ContentBlock *flowOutClaudeBlock   `json:"content_block,omitempty"`
	Delta        *flowOutClaudeDelta   `json:"delta,omitempty"`
	Usage        *flowOutClaudeUsage   `json:"usage,omitempty"`
}

func flowOutClaude(reply flowOutReply, stream bool) *flowOutFrames {
	frames := &flowOutFrames{}
	blocks := []flowOutClaudeBlock{}
	for _, candidate := range reply.candidates {
		for _, part := range candidate.parts {
			blocks = append(blocks, flowOutClaudeBlock{"text", part.content()})
		}
	}
	stop := "end_turn"
	if reply.candidates[0].finish == "MAX_TOKENS" {
		stop = "max_tokens"
	}
	message := func(content []flowOutClaudeBlock, reason *string) *flowOutClaudeMessage {
		return &flowOutClaudeMessage{"msg_flow_" + reply.id, "message", "assistant", reply.model, content, reason, nil, flowOutClaudeUsage{}, reply.media}
	}
	if !stream {
		frames.add("", message(blocks, &stop))
		return frames
	}
	frames.add("message_start", flowOutClaudeEvent{Type: "message_start", Message: message([]flowOutClaudeBlock{}, nil)})
	for index, block := range blocks {
		frames.add("content_block_start", flowOutClaudeEvent{Type: "content_block_start", Index: ptr(index), ContentBlock: &flowOutClaudeBlock{"text", ""}})
		frames.add("content_block_delta", flowOutClaudeEvent{Type: "content_block_delta", Index: ptr(index), Delta: &flowOutClaudeDelta{Type: "text_delta", Text: block.Text}})
		frames.add("content_block_stop", flowOutClaudeEvent{Type: "content_block_stop", Index: ptr(index)})
	}
	frames.add("message_delta", flowOutClaudeEvent{Type: "message_delta", Delta: &flowOutClaudeDelta{StopReason: &stop}, Usage: &flowOutClaudeUsage{}})
	frames.add("message_stop", flowOutClaudeEvent{Type: "message_stop"})
	return frames
}
