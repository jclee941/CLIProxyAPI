package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Generation uses a different envelope from the capability RPC on the same host:
// a separate path, no rpcids, a two-element f.req, and a header that carries the
// model selection. The model is chosen by capability id and mode; its name never
// travels on the wire.

const webGeneratePath = "/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate"

// webThinkingDefault is the depth the flash capability is driven with.
const webThinkingDefault = 4

// webCapacityFlag marks an account allowed the higher generation capacity.
const webCapacityFlag = 8

// webGenerationFields lays out the positional request body. Each index is a
// protocol slot whose meaning is carried by position alone, so the indexes are
// reproduced exactly rather than named.
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
	fields[45] = 1
	fields[53] = 0
	fields[59] = conversationID
	fields[61] = []any{}
	fields[68] = 1
	fields[79] = mode
	return fields
}

// webSelectionHeader carries the model selection. This is the only place the
// capability id and mode reach the server; the request body repeats the mode but
// never names a model.
func webSelectionHeader(model capability, capacity int) string {
	header := []any{1, nil, nil, nil, model.CapabilityID, nil, nil, 0, []any{4, 5, 6, 8}, nil, nil, capacity, nil, nil, model.Mode}
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

// generateText runs one turn against the selected capability and returns the
// reply text.
func (session *webSession) generateText(ctx context.Context, prompt string, account webAccount, model capability, attachments []webAttachment) (string, error) {
	if session.xsrf == "" {
		if err := session.bootstrap(ctx); err != nil {
			return "", err
		}
	}
	conversationID, err := webConversationID()
	if err != nil {
		return "", err
	}
	fields, err := json.Marshal(webGenerationFields(prompt, model.Mode, webThinkingDefault, conversationID, attachments))
	if err != nil {
		return "", failure(400, "web_request_invalid")
	}
	raw, err := session.postGeneration(ctx, string(fields), account, model)
	if err != nil {
		return "", err
	}
	return webReplyText(raw)
}

// postGeneration sends one generation turn. Text and video differ only in the
// field array, so the envelope and the selection header are shared.
func (session *webSession) postGeneration(ctx context.Context, fields string, account webAccount, model capability) ([]byte, error) {
	envelope, err := json.Marshal([]any{nil, fields})
	if err != nil {
		return nil, failure(400, "web_request_invalid")
	}
	query := url.Values{
		"bl":     {session.build},
		"hl":     {"en"},
		"_reqid": {strconv.Itoa(session.requestID)},
		"rt":     {"c"},
	}
	if session.sessionID != "" {
		query.Set("f.sid", session.sessionID)
	}
	session.requestID += 100000
	body := url.Values{"f.req": {string(envelope)}, "at": {session.xsrf}}.Encode()
	overrides := http.Header{
		"x-goog-ext-525001261-jspb": {webSelectionHeader(model, webCapacity(account.CapacityFlags))},
		"x-goog-ext-73010990-jspb":  {"[0,0,0]"},
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
	return text, nil
}

// webConversationID is the per-turn identifier the protocol expects in slot 59.
func webConversationID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", failure(500, "web_request_invalid")
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}
