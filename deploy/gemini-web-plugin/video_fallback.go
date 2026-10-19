package main

import (
	"context"
	"encoding/json"
	"errors"
)

// proseFallbackEligible reports whether a video turn may be made again on the
// standard mode once the product has answered it: the request named no mode,
// the mode configured as the default is not the standard one, and the reply came
// as prose, which names its language in candidate slot 9. From 22:35 KST on
// 2026-10-19 4 Argon answered nearly every video turn with the product's
// text-model reply while 3.8 Flash made the same requests' videos in the same
// minute, so the operator chose to make such a turn on the standard mode at once
// (2026-10-20). A mode the caller named is never replaced, and a reply with no
// language came from the video tool itself, which another model would meet too.
func proseFallbackEligible(requested int, used capability, diag *videoTurnDiag) bool {
	return requested == 0 && used.Mode != 1 && diag != nil && diag.hasS9v
}

// standardModeFallback is the capability an eligible turn is made on again, and
// whether to make it, given how the turn ended. Only a turn that ended without a
// video is sent again, and not when the product named a limit, which holds on
// every model.
func standardModeFallback(account webAccount, outcome error) (capability, bool) {
	var public *publicError
	if !errors.As(outcome, &public) || public.Code != "no_video_generated" || webVideoLimitReply(public.Message) {
		return capability{}, false
	}
	return webVideoCapability(account)
}

// settledOutcome reads a submitted turn back once and reports how it ended: nil
// while it is still producing a video or has one, its refusal, or why it could
// not be read. The stream can close before the reply settles, so the
// conversation read decides, as it does for the turn's own result.
func (session *webSession) settledOutcome(ctx context.Context, turn continuationTurn) error {
	turns, err := session.rpc(ctx, webVideoTurnsRPC, []any{turn.Conversation, 32, nil, 1, []any{1}, []any{4}, nil, 1})
	if err != nil {
		return err
	}
	candidate, err := continuationCandidate(turn, turns)
	if err != nil {
		return err
	}
	_, err = webParseVideoCandidate(candidate)
	return err
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
