package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"time"
)

const (
	filesResumablePath  = "/upload/v1beta/files/resumable"
	filesUploadLifetime = 24 * time.Hour
	filesUploadAAD      = "gemini-web-files-upload-capability-v1\x00POST\x00" + filesResumablePath
)

// Deliberately excludes body and credentials. Authentication only decrypts the
// capability and reads its encrypted metadata; it never contacts Drive.
type filesUploadAuthRequest struct {
	Method, Path, RawPath, RawQuery string
	Query                           url.Values
}

type filesUploadAuthentication struct {
	Authenticated bool
	CallerScope   string        `json:"caller_scope,omitempty"`
	Rejection     *httpResponse `json:",omitempty"`
}

type filesUploadClaims struct {
	CallerScope string `json:"caller_scope"`
	FileID      string `json:"file_id"`
	ExpiresAt   int64  `json:"expires_at"`
}

func filesUploadToken(request filesUploadAuthRequest) (string, error) {
	if request.Method != "POST" || request.Path != filesResumablePath || request.RawPath != "" && request.RawPath != filesResumablePath {
		return "", failure(401, "file_upload_capability_invalid")
	}
	values := request.Query["upload_id"]
	if len(values) != 1 || values[0] == "" || len(values[0]) > 512 {
		return "", failure(401, "file_upload_capability_invalid")
	}
	if request.RawQuery != "" {
		query, err := url.ParseQuery(request.RawQuery)
		if err != nil || len(query["upload_id"]) != 1 || query.Get("upload_id") != values[0] {
			return "", failure(401, "file_upload_capability_invalid")
		}
	}
	return values[0], nil
}

func (store *sessionStore) filesNewUploadLocked(caller, id string, now time.Time) (string, error) {
	if err := store.filesReadyLocked(); err != nil {
		return "", err
	}
	plaintext, err := json.Marshal(filesUploadClaims{CallerScope: caller, FileID: id, ExpiresAt: now.Add(filesUploadLifetime).Unix()})
	if err != nil {
		return "", err
	}
	defer clear(plaintext)
	nonce := make([]byte, store.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", failure(500, "files_nonce_failed")
	}
	raw := store.aead.Seal(nonce, nonce, plaintext, []byte(filesUploadAAD))
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (store *sessionStore) filesUploadClaimsLocked(token string, now time.Time) (filesUploadClaims, error) {
	var claims filesUploadClaims
	if err := store.filesReadyLocked(); err != nil {
		return claims, err
	}
	if token == "" || len(token) > 512 {
		return claims, failure(401, "file_upload_capability_invalid")
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	n := store.aead.NonceSize()
	if err != nil || len(raw) < n+store.aead.Overhead() || base64.RawURLEncoding.EncodeToString(raw) != token {
		return claims, failure(401, "file_upload_capability_invalid")
	}
	plaintext, err := store.aead.Open(nil, raw[:n], raw[n:], []byte(filesUploadAAD))
	if err != nil {
		return claims, failure(401, "file_upload_capability_invalid")
	}
	defer clear(plaintext)
	if strictJSON(plaintext, &claims) != nil {
		return filesUploadClaims{}, failure(401, "file_upload_capability_invalid")
	}
	_, validID := filesReferenceID("files/" + claims.FileID)
	if !accountDigestPattern.MatchString(claims.CallerScope) || !validID || claims.ExpiresAt <= 0 || claims.ExpiresAt > now.Add(filesUploadLifetime).Unix() {
		return filesUploadClaims{}, failure(401, "file_upload_capability_invalid")
	}
	if now.Unix() >= claims.ExpiresAt {
		return filesUploadClaims{}, failure(410, "file_upload_expired")
	}
	return claims, nil
}

func (store *sessionStore) filesUploadCapabilityLocked(token string, now time.Time) (filesUploadClaims, fileRecord, error) {
	claims, err := store.filesUploadClaimsLocked(token, now)
	if err != nil {
		return filesUploadClaims{}, fileRecord{}, err
	}
	record, err := store.filesReadRecordLocked(claims.CallerScope, claims.FileID)
	if err != nil {
		return filesUploadClaims{}, fileRecord{}, err
	}
	if record.UploadID != token || record.File.Source != "UPLOADED" || record.File.State == "DELETING" {
		return filesUploadClaims{}, fileRecord{}, failure(404, "file_not_found")
	}
	return claims, record, nil
}

func (service *service) filesAuthenticateUpload(raw []byte) filesUploadAuthentication {
	reject := func(err error) filesUploadAuthentication {
		response := filesHTTPError(err, true)
		return filesUploadAuthentication{Rejection: &response}
	}
	var request filesUploadAuthRequest
	if json.Unmarshal(raw, &request) != nil {
		return reject(failure(401, "file_upload_capability_invalid"))
	}
	token, err := filesUploadToken(request)
	if err != nil {
		return reject(err)
	}
	store := service.localStore()
	if store == nil {
		return reject(failure(503, "files_requires_session_store"))
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	claims, _, err := store.filesUploadCapabilityLocked(token, service.now())
	if err != nil {
		return reject(err)
	}
	return filesUploadAuthentication{Authenticated: true, CallerScope: claims.CallerScope}
}
