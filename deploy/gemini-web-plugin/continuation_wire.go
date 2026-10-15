package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var continuationIdentifier = regexp.MustCompile(`^(c|r|rc)_[A-Za-z0-9_-]{1,128}$`)

func continuationFrame(turn continuationTurn, raw []byte) (continuationTurn, error) {
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
		return turn, nil
	}
	var entries []any
	if json.Unmarshal(raw, &entries) != nil {
		return turn, failure(502, "invalid_upstream_frame")
	}
	payload := false
	for _, entry := range entries {
		if jsonField(entry, 0) != "wrb.fr" {
			continue
		}
		// The decoder also needs the payload slot to hold an encoded body, and
		// disagreeing with it here is what made one silent frame fatal: a line
		// that carries no receipt was read as a corrupt stream and discarded a
		// generation that had run for ten minutes, when the next line along may
		// still be the one that names the operation.
		if _, encoded := jsonField(entry, 2).(string); encoded {
			payload = true
		}
	}
	if !payload {
		return turn, nil
	}
	frames, err := webResponseFrames(raw)
	if err != nil {
		return turn, err
	}
	metadata := make([]any, 10)
	if turn.Metadata != "" && json.Unmarshal([]byte(turn.Metadata), &metadata) != nil {
		return turn, failure(503, "continuation_store_corrupt")
	}
	for _, frame := range frames {
		if values, ok := jsonField(frame, 1).([]any); ok {
			if len(values) > len(metadata) {
				return turn, failure(502, "invalid_continuation_metadata")
			}
			for i, value := range values {
				if value != nil {
					metadata[i] = value
				}
			}
		}
		if candidate, ok := jsonField(frame, 4, 0, 0).(string); ok {
			metadata[2] = candidate
		}
		if context, ok := jsonField(frame, 25).(string); ok {
			metadata[9] = context
		}
	}
	values := []*string{&turn.Conversation, &turn.Reply, &turn.Candidate}
	prefixes := []string{"c_", "r_", "rc_"}
	for index, target := range values {
		if value, ok := metadata[index].(string); ok && value != "" {
			if !continuationIdentifier.MatchString(value) || !strings.HasPrefix(value, prefixes[index]) {
				return turn, failure(502, "invalid_continuation_metadata")
			}
			// One submission revises its own identifiers as the video candidate moves
			// from placeholder to ready, so the newest value wins here exactly as it
			// does in generateVideo. Cross-operation drift is caught by the parent
			// conversation check below and by continuationCandidate on re-read.
			*target = value
		}
	}
	if turn.Parent != "" {
		var parent []any
		if json.Unmarshal([]byte(turn.Parent), &parent) != nil {
			return turn, failure(503, "continuation_store_corrupt")
		}
		inherited, named := jsonField(parent, 0).(string)
		switch {
		case turn.Conversation == "" && named:
			// A turn appended to a conversation that already exists is answered
			// without one: the product names a conversation when it opens one,
			// and this turn opened nothing. Read as an operation that went
			// missing, every chained turn failed after its video was made.
			turn.Conversation, metadata[0] = inherited, inherited
		case turn.Conversation != "" && jsonField(parent, 0) != turn.Conversation:
			return turn, failure(502, "continuation_operation_mismatch")
		}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return turn, err
	}
	if len(encoded) > 1024 {
		return turn, failure(502, "continuation_metadata_too_large")
	}
	turn.Metadata = string(encoded)
	return turn, nil
}

// The RPC is recent-first; the reply id is the turn identity, so matching it is
// what stops recovery binding to a newer turn another client appended. Inside that
// reply the candidate id is revised as the video moves from placeholder to ready,
// exactly as generateVideo observes, so the stored id is preferred but its absence
// is not a mismatch: the current candidate of the matched reply is the same turn.
func continuationCandidate(turn continuationTurn, body any) (any, error) {
	entries, ok := jsonField(body, 0).([]any)
	if !ok {
		return nil, failure(502, "continuation_operation_missing")
	}
	for _, entry := range entries {
		if jsonField(entry, 0, 1) != turn.Reply {
			continue
		}
		candidates, ok := jsonField(entry, 3, 0).([]any)
		if !ok || len(candidates) == 0 {
			break
		}
		for _, candidate := range candidates {
			if jsonField(candidate, 0) == turn.Candidate {
				return candidate, nil
			}
		}
		return candidates[0], nil
	}
	return nil, failure(502, "continuation_operation_mismatch")
}

// frameShapes describes the layout of a generation frame and none of its
// content: which slots carry something, and what kind of value each one is. A
// submission that ends without naming its operation otherwise says nothing about
// which slot moved, and the only alternative is another blind ten minute round.
func frameShapes(shapes []string, raw []byte) []string {
	const limit = 12
	if len(shapes) >= limit {
		return shapes
	}
	frames, err := webResponseFrames(raw)
	if err != nil {
		return shapes
	}
	for _, frame := range frames {
		values, ok := frame.([]any)
		if !ok {
			continue
		}
		slots := make([]string, 0, 8)
		for index, value := range values {
			if value != nil {
				slots = append(slots, fmt.Sprintf("%d:%s", index, jsonKind(value, 3)))
			}
		}
		if shapes = append(shapes, strings.Join(slots, " ")); len(shapes) >= limit {
			break
		}
	}
	return shapes
}

// jsonKind names the kind of a decoded value, descending into lists only far
// enough to tell an identifier apart from the structure holding it.
func jsonKind(value any, depth int) string {
	switch typed := value.(type) {
	case string:
		return "str"
	case float64:
		return "num"
	case bool:
		return "bool"
	case map[string]any:
		return "obj"
	case []any:
		if depth == 0 {
			return "[…]"
		}
		kinds := make([]string, 0, len(typed))
		for _, item := range typed {
			if len(kinds) == 4 {
				kinds = append(kinds, "…")
				break
			}
			kinds = append(kinds, jsonKind(item, depth-1))
		}
		return "[" + strings.Join(kinds, ",") + "]"
	}
	return "-"
}

// webStreamCut says a generation stream ended before its body did. The public
// code stays what every caller already handles; the cause and the bytes that did
// arrive ride along, because naming the side that cut the stream needs both.
type webStreamCut struct {
	*publicError
	Cause     error
	Delivered []byte
}

func (cut *webStreamCut) Unwrap() error { return cut.publicError }

// Observe each complete generation line before reading the next. A transport
// interruption after a receipt frame therefore cannot erase its durable handle.
func readContinuationStream(reader io.Reader, observe func([]byte) error) ([]byte, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, 32*1024*1024+1))
	scanner.Buffer(make([]byte, 4096), 32*1024*1024)
	var raw bytes.Buffer
	for scanner.Scan() {
		line := scanner.Bytes()
		if raw.Len()+len(line)+1 > 32*1024*1024 {
			return nil, failure(502, "web_response_too_large")
		}
		if err := observe(line); err != nil {
			return nil, err
		}
		raw.Write(line)
		raw.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, &webStreamCut{publicError: failure(502, "web_response_failed"), Cause: err, Delivered: raw.Bytes()}
	}
	return raw.Bytes(), nil
}
