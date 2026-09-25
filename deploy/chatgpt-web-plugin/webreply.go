package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// webReply is what one turn produced. Model and Effort are what the product
// says it served, which can differ from what was asked once an allowance runs
// out. Complete is set by the stream's own end marker: a stream cut before it
// may have left the answer half written while the turn goes on server-side.
type webReply struct {
	Text           string
	ConversationID string
	Model          string
	Effort         string
	Complete       bool
}

type webStreamMessage struct {
	Author struct {
		Role string `json:"role"`
	} `json:"author"`
	Recipient string  `json:"recipient"`
	Channel   *string `json:"channel"`
	Content   struct {
		ContentType string            `json:"content_type"`
		Parts       []json.RawMessage `json:"parts"`
	} `json:"content"`
	Metadata struct {
		ModelSlug      string `json:"model_slug"`
		ThinkingEffort string `json:"thinking_effort"`
	} `json:"metadata"`
}

// answers reports whether this message is the assistant's text to the user. A
// thinking turn also streams hidden system messages, a reasoning recap and tool
// traffic, and none of that is the reply.
func (message *webStreamMessage) answers() bool {
	if message.Author.Role != "assistant" || message.Content.ContentType != "text" {
		return false
	}
	if message.Recipient != "" && message.Recipient != "all" {
		return false
	}
	return message.Channel == nil || *message.Channel == "" || *message.Channel == "final"
}

func (message *webStreamMessage) firstPart() string {
	if len(message.Content.Parts) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(message.Content.Parts[0], &text) != nil {
		return ""
	}
	return text
}

// webReplyReader folds a turn's events into its reply. Deltas extend whichever
// message was named last; until one is named they are taken as the reply, which
// is the shape a plain answer streams in.
type webReplyReader struct {
	reply   webReply
	current *webStreamMessage
}

func (reader *webReplyReader) feed(payload string) {
	if reader.reply.ConversationID == "" {
		if match := webConversationRE.FindStringSubmatch(payload); len(match) == 2 {
			reader.reply.ConversationID = match[1]
		}
	}
	// Bare strings such as the "v1" encoding marker carry no reply text.
	if !strings.HasPrefix(payload, "{") {
		return
	}
	var event struct {
		Message *webStreamMessage `json:"message"`
		Type    string            `json:"type"`
	}
	if json.Unmarshal([]byte(payload), &event) != nil {
		return
	}
	if event.Type == "message_stream_complete" {
		reader.reply.Complete = true
	}
	if event.Message != nil {
		reader.enter(event.Message)
		return
	}
	reader.fold(json.RawMessage(payload), "")
}

// enter makes a message the target of the deltas that follow. A whole message
// can also arrive after its deltas, so its text replaces the reply only when it
// is longer and cannot discard what was accumulated.
func (reader *webReplyReader) enter(message *webStreamMessage) {
	reader.current = message
	if message.Author.Role == "assistant" && message.Metadata.ModelSlug != "" {
		reader.reply.Model = message.Metadata.ModelSlug
		reader.reply.Effort = message.Metadata.ThinkingEffort
	}
	if text := message.firstPart(); message.answers() && len(text) > len(reader.reply.Text) {
		reader.reply.Text = text
	}
}

// fold applies one delta. The product nests them: an event may carry the
// fragment directly, wrap it in a counter envelope, hold a list of operations,
// or add a whole message, and each level may restate the pointer. Observed
// shapes are {"c":n,"v":{...}}, {"o":"patch","v":[...]}, {"o":"append","p":...,
// "v":"..."}, a bare {"v":"..."} and {"o":"add","p":"","v":{"message":{...}}};
// handling only the flat ones truncated the reply at the first nested delta.
func (reader *webReplyReader) fold(raw json.RawMessage, pointer string) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return
	}
	switch trimmed[0] {
	case '"':
		var fragment string
		if json.Unmarshal(raw, &fragment) == nil && fragment != "" && webTextPointer(pointer) && reader.extendsReply() {
			reader.reply.Text += fragment
		}
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return
		}
		for _, item := range items {
			reader.fold(item, pointer)
		}
	case '{':
		var node struct {
			Pointer *string           `json:"p"`
			Value   json.RawMessage   `json:"v"`
			Message *webStreamMessage `json:"message"`
		}
		if json.Unmarshal(raw, &node) != nil {
			return
		}
		if node.Message != nil {
			reader.enter(node.Message)
			return
		}
		if len(node.Value) == 0 {
			return
		}
		next := pointer
		if node.Pointer != nil && *node.Pointer != "" {
			next = *node.Pointer
		}
		reader.fold(node.Value, next)
	}
}

func (reader *webReplyReader) extendsReply() bool {
	return reader.current == nil || reader.current.answers()
}

// webTextPointer reports whether a JSON pointer addresses the reply text. An
// empty pointer is inherited from an event that carried no path of its own, which
// the product uses for the plain text delta.
func webTextPointer(pointer string) bool {
	return pointer == "" || strings.HasSuffix(pointer, "/parts/0")
}

// webStreamReply reads a turn's event stream. onDelta, when set, receives each
// growth of the reply as it arrives; an error from it stops the read, because
// the caller the rest would go to has left.
func webStreamReply(response *http.Response, onDelta func(string) error) (webReply, error) {
	defer closeBody(response)
	reader := webReplyReader{}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), imagesMaxEventBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			reader.reply.Complete = true
			continue
		}
		if payload == "" {
			continue
		}
		webTraceEvent(payload)
		before := reader.reply.Text
		reader.feed(payload)
		grown, extended := strings.CutPrefix(reader.reply.Text, before)
		if onDelta == nil || !extended || grown == "" {
			continue
		}
		if err := onDelta(grown); err != nil {
			return reader.reply, err
		}
	}
	return reader.reply, nil
}

// webChatTrace turns on a one-line-per-event record of the stream's shape. It is
// a build-time switch because the reply patches are undocumented and the only
// way to learn their shapes is to observe a real turn.
const webChatTrace = false

// webTraceEvent records the structure of one stream event: which keys it carries,
// the patch operation and pointer, and the type of the value. The value itself is
// never recorded, so no reply text reaches the log.
func webTraceEvent(payload string) {
	if !webChatTrace {
		return
	}
	var event map[string]json.RawMessage
	if json.Unmarshal([]byte(payload), &event) != nil {
		fmt.Fprintf(os.Stderr, "WEBCHATDBG non-object len=%d head=%.40q\n", len(payload), payload)
		return
	}
	keys := make([]string, 0, len(event))
	for key := range event {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	operation, pointer, kind := "", "", ""
	_ = json.Unmarshal(event["o"], &operation)
	_ = json.Unmarshal(event["p"], &pointer)
	if raw, present := event["v"]; present {
		trimmed := strings.TrimSpace(string(raw))
		switch {
		case strings.HasPrefix(trimmed, `"`):
			kind = "string"
		case strings.HasPrefix(trimmed, "["):
			kind = "array"
		case strings.HasPrefix(trimmed, "{"):
			kind = "object"
		default:
			kind = "scalar"
		}
	}
	fmt.Fprintf(os.Stderr, "WEBCHATDBG keys=%s o=%q p=%q v=%s\n", strings.Join(keys, ","), operation, pointer, kind)
}

// webFinalReply re-reads the conversation when the stream did not deliver a
// whole answer - a patch shape this plugin does not know, or a stream cut while
// the turn went on server-side - and waits for the newest answer to finish.
func (client *webClient) webFinalReply(ctx context.Context, conversationID string, budget time.Duration) (string, error) {
	if conversationID == "" {
		return "", failure(502, "web_conversation_missing")
	}
	path := "/backend-api/conversation/" + conversationID
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		raw, err := client.call(ctx, http.MethodGet, path,
			client.header(path, map[string]string{"Accept": "application/json"}), nil, "web_conversation_read_failed")
		if err == nil {
			if text := webLatestAssistantText(raw); text != "" {
				return text, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", failure(499, "web_client_disconnected")
		case <-time.After(webPollInterval):
		}
	}
	return "", failure(504, "web_reply_not_ready")
}

// webLatestAssistantText returns the newest answer in the conversation once it
// has finished. The mapping is keyed by message id rather than ordered, so the
// newest turn is chosen by timestamp, and an answer still being written is not
// returned as if it were whole.
func webLatestAssistantText(raw []byte) string {
	var conversation struct {
		Mapping map[string]struct {
			Message *struct {
				webStreamMessage
				CreateTime float64 `json:"create_time"`
				Status     string  `json:"status"`
			} `json:"message"`
		} `json:"mapping"`
	}
	if json.Unmarshal(raw, &conversation) != nil {
		return ""
	}
	text, newest, finished := "", -1.0, false
	for _, node := range conversation.Mapping {
		message := node.Message
		if message == nil || !message.answers() || message.firstPart() == "" {
			continue
		}
		if message.CreateTime >= newest {
			text, newest, finished = message.firstPart(), message.CreateTime, message.Status != "in_progress"
		}
	}
	if !finished {
		return ""
	}
	return text
}
