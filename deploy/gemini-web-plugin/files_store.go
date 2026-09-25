package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
)

// The Files surface cannot promise the Developer API's 2 GiB limit: generation
// accepts at most 100 MiB and the native RPC transport base64-encodes bodies.
const (
	filesMaxBytes      = webUploadLimit
	filesChunkBytes    = 16 * 1024 * 1024
	filesMetadataBytes = 128 * 1024
	filesGranularity   = 256 * 1024
)

var filesStoreFile = regexp.MustCompile(`^files-[0-9a-f]{64}-[0-9a-f]{64}\.meta$`)
var filesIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

type fileResource struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	MIMEType    string `json:"mimeType"`
	SizeBytes   int64  `json:"sizeBytes,string"`
	CreateTime  string `json:"createTime"`
	UpdateTime  string `json:"updateTime"`
	SHA256Hash  string `json:"sha256Hash,omitempty"`
	URI         string `json:"uri"`
	DownloadURI string `json:"downloadUri,omitempty"`
	State       string `json:"state"`
	Source      string `json:"source"`
}

type fileRecord struct {
	File              fileResource `json:"file"`
	UploadID          string       `json:"upload_id,omitempty"`
	GeneratedKey      string       `json:"generated_key,omitempty"`
	Received          int64        `json:"received"`
	DriveID           string       `json:"drive_id"`
	DriveURL          string       `json:"drive_upload_url,omitempty"`
	DriveComplete     bool         `json:"drive_complete,omitempty"`
	FinalizeRequested bool         `json:"finalize_requested,omitempty"`
}

// These locks serialize one file's remote operations without holding the
// credential store mutex over network I/O. Waiting requests can be cancelled.
type fileLease struct {
	available chan struct{}
	users     int
}

type fileLeases struct {
	mu      sync.Mutex
	entries map[string]*fileLease
}

func (leases *fileLeases) acquire(ctx context.Context, key string) (func(), error) {
	leases.mu.Lock()
	if leases.entries == nil {
		leases.entries = make(map[string]*fileLease)
	}
	lease := leases.entries[key]
	if lease == nil {
		lease = &fileLease{available: make(chan struct{}, 1)}
		lease.available <- struct{}{}
		leases.entries[key] = lease
	}
	lease.users++
	leases.mu.Unlock()
	releaseUser := func() {
		leases.mu.Lock()
		defer leases.mu.Unlock()
		lease.users--
		if lease.users == 0 {
			delete(leases.entries, key)
		}
	}
	select {
	case <-ctx.Done():
		releaseUser()
		return nil, ctx.Err()
	case <-lease.available:
		return func() { lease.available <- struct{}{}; releaseUser() }, nil
	}
}

func filesDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func filesScopePrefix(caller string) string {
	return "files-" + filesDigest("gemini-web-files-caller-v1\x00"+caller) + "-"
}

func filesMetadataName(caller, id string) string {
	return filesScopePrefix(caller) + filesDigest("metadata\x00"+id) + ".meta"
}

func filesReferenceID(reference string) (string, bool) {
	id, ok := strings.CutPrefix(reference, "files/")
	return id, ok && filesIDPattern.MatchString(id) && !strings.HasSuffix(id, "-")
}

func filesAAD(caller, name string) []byte {
	return []byte("gemini-web-files-v1\x00" + caller + "\x00" + name)
}

func (store *sessionStore) filesReadyLocked() error {
	if store.closed || store.failed {
		return failure(503, "session_store_unavailable")
	}
	return nil
}

// Only Files metadata is local. Bytes live in private Drive objects. The Drive
// IDs and resumable URLs use the session store's owner lock, AEAD, safe directory
// descriptor and fsync/rename transaction; filenames are opaque scope hashes.
func (store *sessionStore) filesWriteLocked(caller, name string, plaintext []byte) error {
	nonce := make([]byte, store.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return failure(500, "files_nonce_failed")
	}
	raw := store.aead.Seal(nonce, nonce, plaintext, filesAAD(caller, name))
	if err := store.commit(name, raw); err != nil {
		store.failed = true
		return failure(503, "session_write_outcome_unknown_requires_operator")
	}
	return nil
}

func (store *sessionStore) filesReadLocked(caller, name string, limit int64) (_ []byte, err error) {
	fd, err := syscall.Openat(int(store.directory.Fd()), name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if errors.Is(err, syscall.ENOENT) {
		return nil, failure(404, "file_not_found")
	}
	if err != nil {
		return nil, failure(503, "session_file_unavailable")
	}
	file := os.NewFile(uintptr(fd), name)
	defer func() { err = errors.Join(err, file.Close()) }()
	info, statErr := file.Stat()
	var stat syscall.Stat_t
	if statErr != nil || syscall.Fstat(fd, &stat) != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Nlink != 1 {
		return nil, failure(503, "unsafe_session_file")
	}
	bound := limit + int64(store.aead.NonceSize()+store.aead.Overhead())
	raw, err := io.ReadAll(io.LimitReader(file, bound+1))
	n := store.aead.NonceSize()
	if err != nil || len(raw) < n+store.aead.Overhead() || int64(len(raw)) > bound {
		return nil, failure(503, "files_store_corrupt")
	}
	plaintext, err := store.aead.Open(nil, raw[:n], raw[n:], filesAAD(caller, name))
	if err != nil {
		return nil, failure(503, "files_store_integrity_invalid")
	}
	return plaintext, nil
}

func (store *sessionStore) filesReadRecordLocked(caller, id string) (fileRecord, error) {
	return store.filesReadMetadataLocked(caller, filesMetadataName(caller, id))
}

func (store *sessionStore) filesReadMetadataLocked(caller, name string) (fileRecord, error) {
	raw, err := store.filesReadLocked(caller, name, filesMetadataBytes)
	if err != nil {
		return fileRecord{}, err
	}
	defer clear(raw)
	var record fileRecord
	if strictJSON(raw, &record) != nil {
		return record, failure(503, "files_store_corrupt")
	}
	id, valid := filesReferenceID(record.File.Name)
	if !valid || name != filesMetadataName(caller, id) || record.File.URI != record.File.Name || record.File.SizeBytes < 0 || record.File.SizeBytes > filesMaxBytes || !driveFilePattern.MatchString(record.DriveID) {
		return record, failure(503, "files_store_corrupt")
	}
	if record.Received < 0 || record.Received > record.File.SizeBytes {
		return record, failure(503, "files_store_corrupt")
	}
	if record.File.State != "PROCESSING" && record.File.State != "ACTIVE" && record.File.State != "DELETING" || record.File.State == "ACTIVE" && (!record.DriveComplete || record.Received != record.File.SizeBytes) {
		return record, failure(503, "files_store_corrupt")
	}
	if record.File.Source != "UPLOADED" && record.File.Source != "GENERATED" || record.File.Source == "UPLOADED" && (record.UploadID == "" || record.File.DownloadURI != "") || record.File.Source == "GENERATED" && (record.GeneratedKey == "" || record.File.DownloadURI != "/v1beta/"+record.File.Name+":download?alt=media") {
		return record, failure(503, "files_store_corrupt")
	}
	return record, nil
}

func (store *sessionStore) filesWriteRecordLocked(caller string, record fileRecord) error {
	raw, err := json.Marshal(record)
	if err != nil || len(raw) > filesMetadataBytes {
		return failure(413, "files_metadata_too_large")
	}
	defer clear(raw)
	id, _ := filesReferenceID(record.File.Name)
	return store.filesWriteLocked(caller, filesMetadataName(caller, id), raw)
}

func (store *sessionStore) filesReadRecord(caller, id string) (fileRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.filesReadyLocked(); err != nil {
		return fileRecord{}, err
	}
	return store.filesReadRecordLocked(caller, id)
}

func (store *sessionStore) filesWriteRecord(caller string, record fileRecord) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.filesReadyLocked(); err != nil {
		return err
	}
	return store.filesWriteRecordLocked(caller, record)
}

func filesMissing(err error) bool {
	var public *publicError
	return errors.As(err, &public) && public.HTTPStatus == 404
}

func (store *sessionStore) filesListLocked(caller string) (_ []fileResource, err error) {
	fd, err := syscall.Openat(int(store.directory.Fd()), ".", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, failure(503, "session_store_unavailable")
	}
	directory := os.NewFile(uintptr(fd), ".")
	defer func() { err = errors.Join(err, directory.Close()) }()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, failure(503, "session_store_unavailable")
	}
	result := make([]fileResource, 0)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), filesScopePrefix(caller)) || !strings.HasSuffix(entry.Name(), ".meta") {
			continue
		}
		record, err := store.filesReadMetadataLocked(caller, entry.Name())
		if filesMissing(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if record.File.State == "ACTIVE" {
			result = append(result, record.File)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (store *sessionStore) filesRemoveRecord(caller, id string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.filesReadyLocked(); err != nil {
		return err
	}
	if err := syscall.Unlinkat(int(store.directory.Fd()), filesMetadataName(caller, id)); err != nil {
		store.failed = true
		return failure(503, "files_cleanup_failed")
	}
	if err := store.directory.Sync(); err != nil {
		store.failed = true
		return failure(503, "files_cleanup_failed")
	}
	return nil
}

func (store *sessionStore) filesTokenLocked(caller, purpose, value string) (string, error) {
	nonce := make([]byte, store.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", failure(500, "files_nonce_failed")
	}
	raw := store.aead.Seal(nonce, nonce, []byte(value), filesAAD(caller, purpose))
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (store *sessionStore) filesParseTokenLocked(caller, purpose, token string) (string, error) {
	if len(token) > 256 {
		return "", failure(400, "files_token_invalid")
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	n := store.aead.NonceSize()
	if err != nil || len(raw) < n+store.aead.Overhead() {
		return "", failure(400, "files_token_invalid")
	}
	content, err := store.aead.Open(nil, raw[:n], raw[n:], filesAAD(caller, purpose))
	if err != nil {
		return "", failure(404, "file_not_found")
	}
	defer clear(content)
	return string(content), nil
}

func (service *service) filesStore(caller string) (*sessionStore, error) {
	if !accountDigestPattern.MatchString(caller) {
		return nil, failure(401, "files_requires_authenticated_caller_scope")
	}
	store := service.localStore()
	if store == nil {
		return nil, failure(503, "files_requires_session_store")
	}
	return store, nil
}

// resolveFileSource does not fetch a URI or bind an upload to a Google account.
// The generation path chooses its account and uploads this caller's bytes there.
func (service *service) resolveFileSource(caller, reference string) (webSource, error) {
	id, valid := filesReferenceID(reference)
	if !valid {
		return webSource{}, failure(400, "file_reference_invalid")
	}
	store, err := service.filesStore(caller)
	if err != nil {
		return webSource{}, err
	}
	record, err := store.filesReadRecord(caller, id)
	if err != nil {
		return webSource{}, err
	}
	if record.File.State != "ACTIVE" {
		return webSource{}, failure(409, "file_not_active")
	}
	return webSource{MIMEType: record.File.MIMEType, Size: record.File.SizeBytes, Open: func(ctx context.Context) (io.ReadCloser, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		current, err := store.filesReadRecord(caller, id)
		if err != nil {
			return nil, err
		}
		if current.File != record.File || current.DriveID != record.DriveID {
			return nil, failure(409, "file_changed")
		}
		return service.filesDriveOpen(ctx, current)
	}}, nil
}
