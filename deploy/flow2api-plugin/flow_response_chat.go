package main

import "strings"

type flowOutChatImage struct {
	Type     string `json:"type"`
	Index    int    `json:"index"`
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url"`
}
type flowOutChatMessage struct {
	Role    string             `json:"role,omitempty"`
	Content *string            `json:"content,omitempty"`
	Images  []flowOutChatImage `json:"images,omitempty"`
}
type flowOutChatChoice struct {
	Index        int                 `json:"index"`
	Message      *flowOutChatMessage `json:"message,omitempty"`
	Delta        *flowOutChatMessage `json:"delta,omitempty"`
	FinishReason *string             `json:"finish_reason"`
}
type flowOutChatBody struct {
	ID      string                  `json:"id"`
	Object  string                  `json:"object"`
	Created int64                   `json:"created"`
	Model   string                  `json:"model"`
	Choices []flowOutChatChoice     `json:"choices"`
	Flow    []flowOutMediaReference `json:"flow,omitempty"`
}

func flowOutChatFinish(reason string) *string {
	switch reason {
	case "MAX_TOKENS":
		return ptr("length")
	case "SAFETY", "IMAGE_SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII", "RECITATION":
		return ptr("content_filter")
	}
	return ptr("stop")
}

func flowOutChatMessageOf(candidate flowOutCandidate) *flowOutChatMessage {
	message := &flowOutChatMessage{Role: "assistant"}
	var content []string
	for _, part := range candidate.parts {
		if !part.isImage() {
			content = append(content, part.content())
			continue
		}
		image := flowOutChatImage{Type: "image_url", Index: len(message.Images)}
		image.ImageURL.URL = part.content()
		message.Images = append(message.Images, image)
	}
	if len(content) > 0 {
		message.Content = ptr(strings.Join(content, "\n"))
	}
	return message
}

func flowOutChat(reply flowOutReply, stream bool) *flowOutFrames {
	frames := &flowOutFrames{}
	body := func(object string, choice flowOutChatChoice) flowOutChatBody {
		return flowOutChatBody{reply.id, object, reply.created, reply.model, []flowOutChatChoice{choice}, reply.media}
	}
	if !stream {
		choices := make([]flowOutChatChoice, len(reply.candidates))
		for i, candidate := range reply.candidates {
			choices[i] = flowOutChatChoice{Index: i, Message: flowOutChatMessageOf(candidate), FinishReason: flowOutChatFinish(candidate.finish)}
		}
		frames.add("", flowOutChatBody{"chatcmpl-flow-" + reply.id, "chat.completion", reply.created, reply.model, choices, reply.media})
		return frames
	}
	for i, candidate := range reply.candidates {
		first := body("chat.completion.chunk", flowOutChatChoice{Index: i, Delta: flowOutChatMessageOf(candidate)})
		last := body("chat.completion.chunk", flowOutChatChoice{Index: i, Delta: &flowOutChatMessage{}, FinishReason: flowOutChatFinish(candidate.finish)})
		first.ID, last.ID = "chatcmpl-flow-"+reply.id, "chatcmpl-flow-"+reply.id
		frames.add("", first)
		frames.add("", last)
	}
	return frames
}
