package main

import (
	"crypto/sha256"
	"slices"
)

type videoWireMode string

const (
	videoWireLegacy    videoWireMode = "legacy"
	videoWireWeb       videoWireMode = "web"
	videoWireAlternate videoWireMode = "alternate"
)

// variant selects only the three account-tier fields observed in the web app.
// The key is the first local receipt, since Google names the chat only after
// submission. Continuations persist and inherit the result, not the turn nonce.
func (mode videoWireMode) variant(key string, flags []int) videoWireMode {
	if !slices.Contains(flags, 16) {
		return videoWireLegacy
	}
	switch mode {
	case videoWireWeb:
		return videoWireWeb
	case videoWireAlternate:
		digest := sha256.Sum256([]byte(key))
		if digest[0]&1 == 0 {
			return videoWireWeb
		}
	}
	return videoWireLegacy
}

func (turn continuationTurn) wireMode(mode videoWireMode) videoWireMode {
	if mode != videoWireAlternate {
		return mode
	}
	if turn.VideoWire != "" {
		return turn.VideoWire
	}
	// Chats submitted before this switch existed used legacy bytes.
	if turn.Parent != "" {
		return videoWireLegacy
	}
	return videoWireAlternate
}

func (variant videoWireMode) fields(fields []any) []any {
	if variant == videoWireWeb {
		fields[30] = []any{4, 16}
	}
	return fields
}
