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
	if conversation == "" || reply == "" {
		return "", false, nil
	}
	context, newest, answered := "", uint64(0), false
	for other, turn := range turns {
		if other == key || turn.Conversation != conversation {
			continue
		}
		if turn.Reply != "" && turn.Parent != "" {
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
