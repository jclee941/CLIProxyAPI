package main

import "encoding/json"

// webRequestEdit is the request kind the web app writes to slot 72 when it
// resends a turn with new text: the prompt edit it confirms with Update. Such a
// resend addresses the edited turn's parent reply in slot 2.
const webRequestEdit = 2

// continuationEdit says whether a turn sent from parent replaces a turn the
// conversation already answered from that parent, and the conversation's newest
// context, which the web app sends with every edit. The product answers the edit
// in place of the old turn: the conversation then reads without it.
func continuationEdit(turns map[string]continuationTurn, key string, parent []any) (string, bool, error) {
	conversation, _ := jsonField(parent, 0).(string)
	reply, _ := jsonField(parent, 1).(string)
	if conversation == "" {
		return "", false, nil
	}
	// A parent that names a conversation and no reply is the first message of
	// that conversation, which the web app edits in place: nothing answered it
	// from a parent, and the edit is addressed to the conversation alone.
	first := reply == ""
	context, newest, answered := "", uint64(0), first
	for other, turn := range turns {
		if other == key || turn.Conversation != conversation {
			continue
		}
		if !first && turn.Reply != "" && turn.Parent != "" {
			var from []any
			if json.Unmarshal([]byte(turn.Parent), &from) != nil {
				return "", false, failure(503, "continuation_store_corrupt")
			}
			answered = answered || jsonField(from, 1) == reply
		}
		if turn.Metadata == "" {
			continue
		}
		var metadata []any
		if json.Unmarshal([]byte(turn.Metadata), &metadata) != nil {
			return "", false, failure(503, "continuation_store_corrupt")
		}
		if value, ok := jsonField(metadata, 9).(string); ok && value != "" && turn.Sequence >= newest {
			context, newest = value, turn.Sequence
		}
	}
	if answered && len(parent) < 10 {
		return "", false, failure(503, "continuation_store_corrupt")
	}
	return context, answered, nil
}

// refusedFirstTurnAddress says whether turn is a conversation's first message
// that ended without a video, and the parent a retry of it is sent from. The
// product lets the first message be edited in place, which is how a text-model
// answer to it is turned into a video; a retry that opened a new chat instead
// would leave the refused message standing. The retry is addressed to the same
// conversation with no reply and no choice, the layout the web app uses for an
// edit of a turn with no parent. A retry of that retry has the same address, so
// naming the newest failed attempt keeps editing the same first message.
func refusedFirstTurnAddress(turn continuationTurn) (string, bool, error) {
	refused := turn.State == "no_video" || turn.State == "failed" && turn.Error == "no_video_generated"
	if !refused || turn.Conversation == "" {
		return "", false, nil
	}
	if turn.Parent != "" {
		var parent []any
		if json.Unmarshal([]byte(turn.Parent), &parent) != nil {
			return "", false, failure(503, "continuation_store_corrupt")
		}
		if jsonField(parent, 1) != "" {
			return "", false, nil
		}
	}
	address := make([]any, 10)
	address[0], address[1], address[2] = turn.Conversation, "", ""
	encoded, err := json.Marshal(address)
	return string(encoded), true, err
}
