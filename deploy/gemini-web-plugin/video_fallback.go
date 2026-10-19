package main

import (
	"encoding/json"
	"errors"
)

// standardModeFallback is the capability a video turn is made on again, and
// whether to make it, after the mode configured as the default answered the turn
// in prose instead of calling the video tool. From 22:35 KST on 2026-10-19 4 Argon
// answered nearly every video turn with the product's text-model reply while 3.8
// Flash made the same requests' videos in the same minute, so the operator chose
// to make such a turn on the standard mode at once (2026-10-20). A mode the caller
// named is never replaced. A reply with no language in slot 9 came from the video
// tool itself, and a limit the product named holds on every model, so neither is
// sent again.
func standardModeFallback(requested int, used capability, account webAccount, diag *videoTurnDiag, refusal error) (capability, bool) {
	var public *publicError
	if requested != 0 || used.Mode == 1 || diag == nil || !diag.hasS9v ||
		!errors.As(refusal, &public) || public.Code != "no_video_generated" || webVideoLimitReply(public.Message) {
		return capability{}, false
	}
	return webVideoCapability(account)
}

// fallbackAddress is where a turn made again on the standard mode is sent: in
// place of the attempt the product refused, the way the web app sends an edit. A
// turn with a parent goes from that parent again, and a conversation's first
// message is addressed to the conversation alone. The edit carries the newest
// context, the one the refused attempt returned.
func fallbackAddress(parent string, refused continuationTurn) ([]any, error) {
	address := make([]any, 10)
	if parent != "" {
		if json.Unmarshal([]byte(parent), &address) != nil || len(address) < 10 {
			return nil, failure(503, "continuation_store_corrupt")
		}
	} else {
		address[0], address[1], address[2] = refused.Conversation, "", ""
	}
	var metadata []any
	if refused.Metadata != "" && json.Unmarshal([]byte(refused.Metadata), &metadata) != nil {
		return nil, failure(503, "continuation_store_corrupt")
	}
	if context, ok := jsonField(metadata, 9).(string); ok && context != "" {
		address[9] = context
	}
	return address, nil
}

// lastFrameCandidate is the newest candidate a generation line carries, or the
// one seen before it when the line carries none.
func lastFrameCandidate(raw []byte, seen any) any {
	frames, err := webResponseFrames(raw)
	if err != nil {
		return seen
	}
	for _, frame := range frames {
		if candidate := jsonField(frame, 4, 0); candidate != nil {
			seen = candidate
		}
	}
	return seen
}
