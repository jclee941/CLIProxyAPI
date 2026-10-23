package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Generation uses a different envelope from the capability RPC on the same host:
// a separate path, no rpcids, a two-element f.req, and a header that carries the
// model selection. The model is chosen by capability id and mode; its name never
// travels on the wire.

const webGeneratePath = "/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate"

// webThinkingDefault is what slot 17 carries on a text turn: 0, as the web app
// and every video turn send it. The 4 sent until 2026-10-15 is MEDIUM in the
// web app's thinking enum, which 3.1 Pro answered on every account with a
// generic error or a stream that never came; the thinking level a caller asks
// for travels in slot 80 and the selection header.
const webThinkingDefault = 0

// webCapacityFlag marks an account allowed the higher generation capacity.
const webCapacityFlag = 8

// webGenerationFields lays out the positional request body. Each index is a
// protocol slot whose meaning is carried by position alone, so the indexes are
// reproduced exactly rather than named. The layout follows a text turn the web
// app sent on 2026-10-15. Slot 45 is its temporary chat flag (protocol field
// 46), which an ordinary turn leaves unset; it had been sent on every text turn
// since the port, which kept those chats out of history and away from tools.
func webGenerationFields(prompt string, mode, thinking int, conversationID string, attachments []webAttachment) []any {
	fields := make([]any, 102)
	fields[0] = []any{prompt, 0, nil, webAttachmentSlot(attachments), nil, nil, 0}
	fields[1] = []any{"en"}
	fields[2] = []any{"", "", "", nil, nil, nil, nil, nil, nil, ""}
	fields[6] = []any{0}
	fields[7] = 1
	fields[10] = 1
	fields[11] = 0
	fields[17] = []any{[]any{thinking}}
	fields[18] = 0
	fields[27] = 1
	fields[30] = []any{4}
	fields[41] = []any{1}
	fields[49] = 0
	fields[53] = 0
	fields[59] = conversationID
	fields[61] = []any{}
	fields[68] = 1
	fields[79] = mode
	fields[91] = 0
	fields[96] = 0
	fields[98] = 1
	return fields
}

// webSelectionHeader carries the model selection. This is the only place the
// capability id and mode reach the server; the request body repeats the mode but
// never names a model.
func webSelectionHeader(model capability, capacity, thinkingLevel int, instance, build string) string {
	header := []any{1, nil, nil, nil, model.CapabilityID, nil, nil, 0, []any{4, 5, 6, 8}, nil, nil, capacity, nil, nil, model.Mode}
	if capacity == 3 {
		header[8] = []any{4, 5, 6, 8, 16, 4, 5, 6, 8, 16}
	}
	// The mode is followed by the thinking level a text turn asks for, then the
	// web app's client instance and build label.
	if thinkingLevel > 0 || instance != "" && build != "" {
		var level any
		if thinkingLevel > 0 {
			level = thinkingLevel
		}
		header = append(header, level)
	}
	if instance != "" && build != "" {
		header = append(header, instance, []any{build})
	}
	encoded, err := json.Marshal(header)
	if err != nil {
		return webModelHeader
	}
	return string(encoded)
}

func webCapacity(flags []int) int {
	for _, flag := range flags {
		if flag == webCapacityFlag {
			return 2
		}
	}
	return 1
}

// webReplyConversation reads the conversation a reply names, "c_..." in slot 1
// of its body.
func webReplyConversation(raw []byte) string {
	bodies, err := webResponseFrames(raw)
	if err != nil {
		return ""
	}
	for _, body := range bodies {
		if conversation, ok := jsonField(body, 1, 0).(string); ok && conversation != "" {
			return conversation
		}
	}
	return ""
}

// sendTurn submits one turn that opens a conversation and returns the reply
// stream as it arrived. A thinking level of 0 leaves the mode's own.
func (session *webSession) sendTurn(ctx context.Context, prompt string, account webAccount, model capability, attachments []webAttachment, thinkingLevel int) ([]byte, error) {
	if session.xsrf == "" {
		if err := session.bootstrap(ctx); err != nil {
			return nil, err
		}
	}
	conversationID, err := webConversationID()
	if err != nil {
		return nil, err
	}
	layout := webGenerationFields(prompt, model.Mode, session.tuned.textThinkingSlot(), conversationID, attachments)
	if thinkingLevel > 0 {
		layout[80] = thinkingLevel
	}
	fields, err := json.Marshal(layout)
	if err != nil {
		return nil, failure(400, "web_request_invalid")
	}
	return session.postGeneration(ctx, string(fields), account, model, webLanguageDefault, conversationID, thinkingLevel)
}

// postGeneration sends one generation turn. Text and video differ only in the
// field array and the interface language, so the envelope and the selection
// header are shared.
func (session *webSession) postGeneration(ctx context.Context, fields string, account webAccount, model capability, language, turn string, thinkingLevel int) ([]byte, error) {
	envelope, err := json.Marshal([]any{nil, fields})
	if err != nil {
		return nil, failure(400, "web_request_invalid")
	}
	query := url.Values{
		"bl":     {session.build},
		"hl":     {language},
		"_reqid": {strconv.Itoa(session.requestID)},
		"rt":     {"c"},
	}
	if session.sessionID != "" {
		query.Set("f.sid", session.sessionID)
	}
	session.requestID += 100000
	body := url.Values{"f.req": {string(envelope)}, "at": {session.xsrf}}.Encode()
	capacity := webCapacity(account.CapacityFlags)
	if session.videoWire == videoWireWeb {
		capacity = 3
	}
	if session.diag != nil {
		session.diag.wire, session.diag.capacity = session.videoWire, capacity
	}
	// A text turn names its turn in the 525005358 header and follows the
	// selection header's mode with the client instance and build label, as the
	// web app does. A video turn passes no turn and sends neither: once it did on
	// 2026-10-15, its generic error answers ("Sorry, something went wrong") rose
	// from 7% to 28% of turns, so it keeps the shape it ran on before.
	instance, build := session.instance, session.build
	if turn == "" {
		instance, build = "", ""
	}
	overrides := http.Header{
		"x-goog-ext-525001261-jspb": {webSelectionHeader(model, capacity, thinkingLevel, instance, build)},
		"x-goog-ext-73010990-jspb":  {"[0,0,0]"},
	}
	if turn != "" {
		overrides["x-goog-ext-525005358-jspb"] = []string{webTurnHeader(turn)}
	}
	return session.do(ctx, session.prefix+webGeneratePath+"?"+query.Encode(), []byte(body), overrides)
}

// webReplyText scans the streamed frames for the reply. Generation frames are not
// length-prefixed the way the capability RPC's are, so the lines are filtered by
// shape instead, and the last non-empty value wins because the stream refines its
// answer as it goes.
func webReplyText(raw []byte) (string, error) {
	text := ""
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "[") {
			continue
		}
		var entries []any
		if json.Unmarshal([]byte(trimmed), &entries) != nil {
			continue
		}
		for _, entry := range entries {
			if err := webFrameError(entry); err != nil {
				return "", err
			}
			if jsonField(entry, 0) != "wrb.fr" {
				continue
			}
			encoded, ok := jsonField(entry, 2).(string)
			if !ok {
				continue
			}
			var decoded any
			if json.Unmarshal([]byte(encoded), &decoded) != nil {
				continue
			}
			found = true
			if value, ok := jsonField(decoded, 4, 0, 1, 0).(string); ok && value != "" {
				text = value
			}
		}
	}
	if !found {
		return "", failure(502, "web_response_missing")
	}
	if text == "" {
		return "", failure(502, "web_response_invalid")
	}
	return webCodeFence.ReplaceAllString(text, "```$1"), nil
}

// webCodeFence is the info string the web app gives the code it ran and that
// code's output ("python?code_reference&code_event_index=1", "text?code_stdout
// &..."); its renderer reads the suffix, which a markdown reader shows as text.
var webCodeFence = regexp.MustCompile("```(\\w*)\\?code_[a-z_]+&code_event_index=\\d+")

// webGenericErrors are the web app's stock answers to a turn it failed. They
// arrive as an ordinary reply, so they would reach a caller as the model's
// answer; on 2026-10-23 every account's 3.8 Flash text turns ended this way for
// hours while 4 Argon answered.
var webGenericErrors = map[string]bool{
	"Sorry, something went wrong. Please try your request again.":                                 true,
	"I'm having a hard time fulfilling your request. Can I help you with something else instead?": true,
	"I encountered an error doing what you asked. Could you try again?":                           true,
	"I seem to be encountering an error. Can I try something else for you?":                       true,
}

// webGenericError reports whether a reply is one of those stock answers in full.
func webGenericError(text string) bool {
	return webGenericErrors[strings.ReplaceAll(strings.TrimSpace(text), "\u2019", "'")]
}

// webConversationID is the per-turn identifier the protocol expects in slot 59.
func webConversationID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", failure(500, "web_request_invalid")
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	return fmt.Sprintf("%X-%X-%X-%X-%X", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}

// webVideoTurnID is the slot 59 identifier of a video turn: lowercase, as video
// turns sent it before text turns took the web app's uppercase form.
func webVideoTurnID() (string, error) {
	id, err := webConversationID()
	return strings.ToLower(id), err
}

// webTurnHeader names the turn the request submits, as the web app does next to
// the turn id it puts in slot 59.
func webTurnHeader(turn string) string {
	encoded, err := json.Marshal([]any{turn, 1})
	if err != nil {
		return ""
	}
	return string(encoded)
}
