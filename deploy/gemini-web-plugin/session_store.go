package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
)

type localState string

const (
	localHostPending localState = "host_sync_pending"
	localReady       localState = "ready"
	localRenewing    localState = "renewal_intent"
	localSubmitting  localState = "submission_intent"
)

var localReferencePattern = regexp.MustCompile(`^session://gemini-web/[0-9a-f]{32}$`)

type localSession struct {
	Target             storageRecord        `json:"target"`
	Projection         string               `json:"projection"`
	Previous           storageRecord        `json:"previous"`
	Token              string               `json:"token"`
	Identity           credentialInspection `json:"identity"`
	State              localState           `json:"state"`
	LoginState         string               `json:"login_state,omitempty"`
	LoginExpires       int64                `json:"login_expires,omitempty"`
	LoginTokenHash     [32]byte             `json:"login_token_hash"`
	LegacyRef          string               `json:"legacy_ref,omitempty"`
	LegacyGUID         string               `json:"legacy_guid,omitempty"`
	LegacyUserBound    bool                 `json:"legacy_user_bound,omitempty"`
	LegacyDetached     bool                 `json:"legacy_detached,omitempty"`
	AutoResolvedAt     int64                `json:"auto_resolved_at,omitempty"`
	RotatedAt          int64                `json:"rotated_at,omitempty"`
	Continuations      string               `json:"continuations,omitempty"`
	ContinuationActive string               `json:"continuation_active,omitempty"`
}

type sessionAAD struct {
	Schema    int                  `json:"schema"`
	Purpose   string               `json:"purpose"`
	AccountID string               `json:"account_id"`
	Reference string               `json:"reference"`
	Identity  credentialInspection `json:"identity"`
	Revision  uint64               `json:"revision"`
	State     localState           `json:"state"`
}

type sessionEnvelope struct {
	AAD        sessionAAD `json:"aad"`
	Nonce      []byte     `json:"nonce"`
	Ciphertext []byte     `json:"ciphertext"`
}

type sessionStore struct {
	mu        sync.Mutex
	directory *os.File
	lock      *os.File
	aead      cipher.AEAD
	closed    bool
	failed    bool
	fault     func(string) error
}

func sessionFile(reference string) (string, error) {
	if !localReferencePattern.MatchString(reference) {
		return "", failure(400, "invalid_token_reference")
	}
	return strings.TrimPrefix(reference, "session://gemini-web/") + ".json", nil
}

func openSessionStore(path, encodedKey string) (_ *sessionStore, err error) {
	key, decodeErr := base64.StdEncoding.Strict().DecodeString(encodedKey)
	if decodeErr != nil || len(key) != 32 || base64.StdEncoding.EncodeToString(key) != encodedKey {
		clear(key)
		return nil, failure(400, "invalid_session_key_requires_canonical_base64_32_bytes")
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, failure(500, "session_cipher_failed")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, failure(500, "session_cipher_failed")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return nil, failure(400, "invalid_session_directory")
	}
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, failure(503, "session_directory_unavailable")
	}
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, failure(503, "session_directory_unavailable")
	}
	for _, component := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		next, openErr := syscall.Openat(fd, component, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		var syncErr error
		if openErr == nil {
			syncErr = syscall.Fsync(fd)
		}
		closeErr := syscall.Close(fd)
		if openErr != nil {
			return nil, failure(503, "unsafe_session_directory")
		}
		fd = next
		if closeErr != nil || syncErr != nil {
			return nil, errors.Join(failure(503, "session_directory_unavailable"), syscall.Close(fd))
		}
	}
	directory := os.NewFile(uintptr(fd), path)
	store := &sessionStore{directory: directory, aead: aead}
	defer func() {
		if err != nil {
			err = errors.Join(err, store.close())
		}
	}()
	info, err := directory.Stat()
	if err != nil || info.Mode().Perm() != 0700 {
		return nil, failure(503, "session_directory_requires_0700")
	}
	store.lock, err = store.open("owner.lock", syscall.O_RDWR|syscall.O_CREAT)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(store.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, failure(409, "session_store_already_owned")
	}
	files, err := directory.ReadDir(-1)
	if err != nil {
		return nil, failure(503, "session_store_unavailable")
	}
	for _, file := range files {
		if file.Name() == "owner.lock" {
			continue
		}
		if interactionResultFile.MatchString(file.Name()) || filesStoreFile.MatchString(file.Name()) {
			result, err := store.open(file.Name(), syscall.O_RDONLY)
			if err != nil {
				return nil, err
			}
			if err := result.Close(); err != nil {
				return nil, err
			}
			continue
		}
		if file.Name() == "write.intent" || file.Name() == "write.tmp" {
			return nil, failure(409, "session_write_outcome_unknown_requires_operator")
		}
		reference := "session://gemini-web/" + strings.TrimSuffix(file.Name(), ".json")
		name, nameErr := sessionFile(reference)
		if nameErr != nil || name != file.Name() {
			return nil, failure(503, "session_store_corrupt")
		}
		if _, err := store.readLocked(reference); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (store *sessionStore) open(name string, flags int) (*os.File, error) {
	fd, err := syscall.Openat(int(store.directory.Fd()), name, flags|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, failure(503, "session_file_unavailable")
	}
	file := os.NewFile(uintptr(fd), name)
	info, err := file.Stat()
	var stat syscall.Stat_t
	statErr := syscall.Fstat(fd, &stat)
	if err != nil || statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Nlink != 1 {
		return nil, errors.Join(failure(503, "unsafe_session_file"), file.Close())
	}
	return file, nil
}

func localAAD(record localSession) sessionAAD {
	return sessionAAD{Schema: 1, Purpose: "gemini-web-application-session", AccountID: record.Target.ID, Reference: record.Target.TokenRef, Identity: record.Identity, Revision: record.Target.SessionRevision, State: record.State}
}

func (store *sessionStore) read(reference string) (localSession, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed || store.failed {
		return localSession{}, failure(503, "session_store_unavailable")
	}
	return store.readLocked(reference)
}

func (store *sessionStore) readLocked(reference string) (_ localSession, err error) {
	name, err := sessionFile(reference)
	if err != nil {
		return localSession{}, err
	}
	file, err := store.open(name, syscall.O_RDONLY)
	if err != nil {
		return localSession{}, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	raw, err := io.ReadAll(io.LimitReader(file, 128*1024+1))
	if err != nil || len(raw) > 128*1024 {
		return localSession{}, failure(503, "session_store_corrupt")
	}
	var envelope sessionEnvelope
	if strictJSON(raw, &envelope) != nil || len(envelope.Nonce) != store.aead.NonceSize() {
		return localSession{}, failure(503, "session_store_corrupt")
	}
	aad, err := json.Marshal(envelope.AAD)
	if err != nil {
		return localSession{}, failure(503, "session_store_corrupt")
	}
	plaintext, err := store.aead.Open(nil, envelope.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		return localSession{}, failure(503, "session_key_or_integrity_invalid")
	}
	defer clear(plaintext)
	var record localSession
	if strictJSON(plaintext, &record) != nil || localAAD(record) != envelope.AAD || record.Target.TokenRef != reference || validateLocalSession(record) != nil {
		return localSession{}, failure(503, "session_store_corrupt")
	}
	return record, nil
}

func validateLocalSession(record localSession) error {
	auth, err := authFromRecord(record.Target)
	if err != nil || record.Projection != string(auth.StorageJSON) {
		return failure(503, "session_projection_invalid")
	}
	if _, err := sessionFile(record.Target.TokenRef); err != nil {
		return err
	}
	if record.Target.Type != provider || !accountIDPattern.MatchString(record.Target.ID) || record.Target.SessionRevision == 0 || !accountDigestPattern.MatchString(record.Identity.AccountSHA256) {
		return failure(503, "session_record_invalid")
	}
	token, err := parseToken(record.Token)
	if err != nil {
		return failure(503, "session_record_invalid")
	}
	user, err := tokenAuthUser(token)
	if err != nil || user != record.Identity.AuthUser {
		return failure(503, "session_record_invalid")
	}
	switch record.State {
	case localReady, localHostPending, localRenewing, localSubmitting:
		return nil
	default:
		return failure(503, "session_record_invalid")
	}
}

func (store *sessionStore) write(record localSession) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed || store.failed {
		return failure(503, "session_store_unavailable")
	}
	if err := validateLocalSession(record); err != nil {
		return err
	}
	plaintext, err := json.Marshal(record)
	if err != nil {
		return failure(500, "session_encoding_failed")
	}
	defer clear(plaintext)
	envelope := sessionEnvelope{AAD: localAAD(record), Nonce: make([]byte, store.aead.NonceSize())}
	if _, err := rand.Read(envelope.Nonce); err != nil {
		return failure(500, "session_nonce_failed")
	}
	aad, err := json.Marshal(envelope.AAD)
	if err != nil {
		return failure(500, "session_encoding_failed")
	}
	envelope.Ciphertext = store.aead.Seal(nil, envelope.Nonce, plaintext, aad)
	raw, err := json.Marshal(envelope)
	if err != nil {
		return failure(500, "session_encoding_failed")
	}
	if len(raw) > 128*1024 {
		return failure(413, "session_record_too_large")
	}
	name, err := sessionFile(record.Target.TokenRef)
	if err != nil {
		return err
	}
	if err := store.commit(name, raw); err != nil {
		store.failed = true
		return failure(503, "session_write_outcome_unknown_requires_operator")
	}
	return nil
}

func (store *sessionStore) commit(name string, raw []byte) error {
	if err := store.writeFile("write.intent", []byte("pending\n")); err != nil {
		return err
	}
	if err := store.directory.Sync(); err != nil {
		return err
	}
	if err := store.writeFile("write.tmp", raw); err != nil {
		return err
	}
	if store.fault != nil {
		if err := store.fault("rename"); err != nil {
			return err
		}
	}
	if err := syscall.Renameat(int(store.directory.Fd()), "write.tmp", int(store.directory.Fd()), name); err != nil {
		return err
	}
	if store.fault != nil {
		if err := store.fault("directory_sync"); err != nil {
			return err
		}
	}
	if err := store.directory.Sync(); err != nil {
		return err
	}
	if err := syscall.Unlinkat(int(store.directory.Fd()), "write.intent"); err != nil {
		return err
	}
	return store.directory.Sync()
}

func (store *sessionStore) writeFile(name string, raw []byte) (err error) {
	file, err := store.open(name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if _, err := file.Write(raw); err != nil {
		return err
	}
	if store.fault != nil {
		if err := store.fault("file_sync"); err != nil {
			return err
		}
	}
	return file.Sync()
}

func (store *sessionStore) close() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return nil
	}
	store.closed = true
	store.aead = nil
	var err error
	if store.lock != nil {
		err = store.lock.Close()
	}
	return errors.Join(err, store.directory.Close())
}

func (store *sessionStore) records() ([]localSession, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed || store.failed {
		return nil, failure(503, "session_store_unavailable")
	}
	fd, err := syscall.Openat(int(store.directory.Fd()), ".", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, failure(503, "session_store_unavailable")
	}
	directory := os.NewFile(uintptr(fd), ".")
	files, readErr := directory.ReadDir(-1)
	if err := errors.Join(readErr, directory.Close()); err != nil {
		return nil, failure(503, "session_store_unavailable")
	}
	result := make([]localSession, 0)
	for _, file := range files {
		if file.Name() == "owner.lock" || interactionResultFile.MatchString(file.Name()) || filesStoreFile.MatchString(file.Name()) {
			continue
		}
		record, err := store.readLocked("session://gemini-web/" + strings.TrimSuffix(file.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}
