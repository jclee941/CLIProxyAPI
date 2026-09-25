package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"syscall"
)

var interactionResultFile = regexp.MustCompile(`^interaction-[0-9a-f]{64}\.bin$`)

// Results live outside the small credential record, but use its existing AEAD,
// single-writer lock and fsync/rename transaction. The AAD binds the ciphertext
// to both the account and caller, not merely the filename.
func interactionResultAAD(local localSession, key, caller string) ([]byte, error) {
	return json.Marshal(struct {
		Purpose   string
		Account   string
		Reference string
		Receipt   string
		Caller    string
	}{"gemini-web-interaction-result-v1", local.Target.ID, local.Target.TokenRef, key, caller})
}

func (store *sessionStore) writeInteractionResult(local localSession, key, caller string, body []byte) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed || store.failed {
		return failure(503, "session_store_unavailable")
	}
	aad, err := interactionResultAAD(local, key, caller)
	if err != nil {
		return err
	}
	nonce := make([]byte, store.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return failure(500, "session_nonce_failed")
	}
	raw := store.aead.Seal(nonce, nonce, body, aad)
	if err := store.commit("interaction-"+key+".bin", raw); err != nil {
		store.failed = true
		return failure(503, "session_write_outcome_unknown_requires_operator")
	}
	return nil
}

func (store *sessionStore) readInteractionResult(local localSession, key, caller string) (_ []byte, err error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed || store.failed {
		return nil, failure(503, "session_store_unavailable")
	}
	file, err := store.open("interaction-"+key+".bin", syscall.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	// Native video downloads are already bounded at 512 MiB; this includes base64
	// and the native JSON envelope, without enlarging the credential record limit.
	raw, err := io.ReadAll(io.LimitReader(file, 720*1024*1024+1))
	if err != nil {
		return nil, err
	}
	n := store.aead.NonceSize()
	if len(raw) < n || len(raw) > 720*1024*1024 {
		return nil, failure(503, "interaction_result_corrupt")
	}
	aad, err := interactionResultAAD(local, key, caller)
	if err != nil {
		return nil, err
	}
	body, err := store.aead.Open(nil, raw[:n], raw[n:], aad)
	if err != nil {
		return nil, failure(503, "interaction_result_integrity_invalid")
	}
	return body, nil
}

func (store *sessionStore) removeInteractionResult(key string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed || store.failed {
		return failure(503, "session_store_unavailable")
	}
	err := syscall.Unlinkat(int(store.directory.Fd()), "interaction-"+key+".bin")
	if errors.Is(err, syscall.ENOENT) {
		return nil
	}
	if err != nil {
		return failure(503, "interaction_result_cleanup_failed")
	}
	return store.directory.Sync()
}
