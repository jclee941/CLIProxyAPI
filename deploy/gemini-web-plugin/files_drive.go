package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (service *service) filesDriveOrigin() string {
	if service.driveOverride != "" {
		return service.driveOverride
	}
	return driveOrigin
}

func (service *service) filesDriveAuthorization(ctx context.Context) (string, error) {
	bearer, err := service.driveAuthorization(ctx)
	if err != nil {
		return "", err
	}
	if bearer == "" {
		return "", failure(503, "files_requires_drive_write_oauth")
	}
	return bearer, nil
}

func (service *service) filesRequireWrite(ctx context.Context) error {
	if _, err := service.filesDriveAuthorization(ctx); err != nil {
		return err
	}
	service.driveAccess.mu.Lock()
	scopes := service.driveAccess.scopes
	service.driveAccess.mu.Unlock()
	for _, scope := range strings.Fields(scopes) {
		if scope == "https://www.googleapis.com/auth/drive" || scope == "https://www.googleapis.com/auth/drive.file" {
			return nil
		}
	}
	return failure(503, "files_drive_write_permission_required")
}

// Resumable URLs are private Drive capabilities, never caller-supplied URLs.
// Even a malformed upstream Location cannot redirect an OAuth bearer elsewhere.
func (service *service) filesDriveSessionURL(location string) bool {
	if len(location) > 4096 {
		return false
	}
	parsed, err := url.Parse(location)
	origin, originErr := url.Parse(service.filesDriveOrigin())
	return err == nil && originErr == nil && parsed.Scheme == origin.Scheme && parsed.Host == origin.Host && parsed.User == nil && parsed.Fragment == "" && parsed.Path == "/upload/drive/v3/files" && parsed.RawPath == "" && parsed.Query().Get("upload_id") != "" && parsed.Query().Get("uploadType") == "resumable"
}

func (service *service) filesDriveRequest(ctx context.Context, method, endpoint string, headers http.Header, body []byte) (int, http.Header, []byte, error) {
	bearer, err := service.filesDriveAuthorization(ctx)
	if err != nil {
		return 0, nil, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, failure(500, "files_drive_request_invalid")
	}
	if headers != nil {
		request.Header = headers.Clone()
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	response, err := service.client.Do(request)
	if err != nil {
		return 0, nil, nil, failure(502, "files_drive_outcome_unknown_query_required")
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
	if err := errors.Join(readErr, response.Body.Close()); err != nil || len(raw) > 64*1024 {
		return 0, nil, nil, failure(502, "files_drive_response_invalid")
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return 0, nil, nil, failure(503, "files_drive_write_permission_required")
	}
	return response.StatusCode, response.Header, raw, nil
}

func (service *service) filesDriveAllocate(ctx context.Context) (string, error) {
	if err := service.filesRequireWrite(ctx); err != nil {
		return "", err
	}
	status, _, body, err := service.filesDriveRequest(ctx, "GET", service.filesDriveOrigin()+"/drive/v3/files/generateIds?count=1&space=drive&type=files", nil, nil)
	if err != nil {
		return "", err
	}
	var result struct {
		IDs []string `json:"ids"`
	}
	if status != 200 || json.Unmarshal(body, &result) != nil || len(result.IDs) != 1 || !driveFilePattern.MatchString(result.IDs[0]) {
		return "", failure(502, "files_drive_allocation_failed")
	}
	return result.IDs[0], nil
}

func (service *service) filesDriveBegin(ctx context.Context, caller string, record fileRecord) (string, error) {
	body, err := json.Marshal(map[string]string{
		"id": record.DriveID, "name": "cpa-native-" + filesDigest(caller + "\x00" + record.File.Name)[:32], "mimeType": record.File.MIMEType,
	})
	if err != nil {
		return "", err
	}
	status, headers, _, err := service.filesDriveRequest(ctx, "POST", service.filesDriveOrigin()+"/upload/drive/v3/files?uploadType=resumable&fields=id,size,mimeType,sha256Checksum", http.Header{
		"Content-Type":            {"application/json"},
		"X-Upload-Content-Type":   {record.File.MIMEType},
		"X-Upload-Content-Length": {strconv.FormatInt(record.File.SizeBytes, 10)},
	}, body)
	if err != nil {
		return "", err
	}
	location := headers.Get("Location")
	if status != 200 || !service.filesDriveSessionURL(location) {
		return "", failure(502, "files_drive_upload_start_failed")
	}
	return location, nil
}

func (service *service) filesCreateDriveRecord(ctx context.Context, store *sessionStore, caller string, record fileRecord) (fileRecord, error) {
	id, err := service.filesDriveAllocate(ctx)
	if err != nil {
		return record, err
	}
	record.DriveID = id
	// Persist the preallocated remote ID BEFORE creating any object. Recovery or
	// deletion can always address it without repeating an untracked create.
	if err := store.filesWriteRecord(caller, record); err != nil {
		return record, err
	}
	record.DriveURL, err = service.filesDriveBegin(ctx, caller, record)
	if err != nil {
		return record, err
	}
	return record, store.filesWriteRecord(caller, record)
}

func filesDriveProgress(record fileRecord, status int, headers http.Header, body []byte) (fileRecord, error) {
	if status == 308 {
		received := int64(0)
		if value := headers.Get("Range"); value != "" {
			end, found := strings.CutPrefix(value, "bytes=0-")
			last, err := strconv.ParseInt(end, 10, 64)
			if !found || err != nil || last < 0 || last >= record.File.SizeBytes {
				return record, failure(502, "files_drive_offset_invalid")
			}
			received = last + 1
		}
		if received < record.Received {
			return record, failure(502, "files_drive_offset_regressed")
		}
		record.Received = received
		return record, nil
	}
	if status == 404 || status == 410 {
		return record, failure(410, "file_upload_expired")
	}
	if status != 200 && status != 201 {
		return record, failure(502, "files_drive_upload_rejected")
	}
	var metadata struct {
		ID       string `json:"id"`
		Size     string `json:"size"`
		MIMEType string `json:"mimeType"`
		SHA256   string `json:"sha256Checksum"`
	}
	if json.Unmarshal(body, &metadata) != nil || metadata.ID != record.DriveID || metadata.Size != strconv.FormatInt(record.File.SizeBytes, 10) || metadata.MIMEType != record.File.MIMEType {
		return record, failure(502, "files_drive_metadata_invalid")
	}
	if metadata.SHA256 != "" {
		digest, err := hex.DecodeString(metadata.SHA256)
		if err != nil || len(digest) != sha256.Size {
			return record, failure(502, "files_drive_checksum_invalid")
		}
		hash := base64.StdEncoding.EncodeToString(digest)
		if record.File.SHA256Hash != "" && record.File.SHA256Hash != hash {
			return record, failure(502, "files_drive_checksum_mismatch")
		}
		record.File.SHA256Hash = hash
	}
	record.Received, record.DriveComplete = record.File.SizeBytes, true
	return record, nil
}

func (service *service) filesDriveTransfer(ctx context.Context, record fileRecord, body []byte, query bool) (fileRecord, error) {
	if !service.filesDriveSessionURL(record.DriveURL) {
		return record, failure(409, "file_upload_session_unavailable")
	}
	contentRange := "bytes */" + strconv.FormatInt(record.File.SizeBytes, 10)
	if !query && len(body) > 0 {
		contentRange = "bytes " + strconv.FormatInt(record.Received, 10) + "-" + strconv.FormatInt(record.Received+int64(len(body))-1, 10) + "/" + strconv.FormatInt(record.File.SizeBytes, 10)
	}
	status, headers, raw, err := service.filesDriveRequest(ctx, "PUT", record.DriveURL, http.Header{
		"Content-Type": {"application/octet-stream"}, "Content-Range": {contentRange},
	}, body)
	if err != nil {
		return record, err
	}
	return filesDriveProgress(record, status, headers, raw)
}

func (service *service) filesDelete(ctx context.Context, store *sessionStore, caller, id string) error {
	release, err := service.fileLeases.acquire(ctx, filesMetadataName(caller, id))
	if err != nil {
		return err
	}
	defer release()
	record, err := store.filesReadRecord(caller, id)
	if err != nil {
		return err
	}
	// A durable deletion intent hides this file before touching Drive, and keeps
	// the remote ID available for an idempotent retry after an interrupted delete.
	record.File.State = "DELETING"
	if err := store.filesWriteRecord(caller, record); err != nil {
		return err
	}
	status, _, _, err := service.filesDriveRequest(ctx, "DELETE", service.filesDriveOrigin()+"/drive/v3/files/"+url.PathEscape(record.DriveID), nil, nil)
	if err != nil {
		return err
	}
	if status != 204 && status != 200 && status != 404 {
		return failure(502, "files_drive_delete_failed")
	}
	return store.filesRemoveRecord(caller, id)
}

type filesDriveBody struct {
	io.ReadCloser
	remaining int64
	cancel    context.CancelFunc
}

func (body *filesDriveBody) Read(buffer []byte) (int, error) {
	if int64(len(buffer)) > body.remaining+1 {
		buffer = buffer[:body.remaining+1]
	}
	n, err := body.ReadCloser.Read(buffer)
	body.remaining -= int64(n)
	if body.remaining < 0 || errors.Is(err, io.EOF) && body.remaining != 0 {
		return 0, failure(502, "files_drive_content_size_mismatch")
	}
	return n, err
}

func (body *filesDriveBody) Close() error {
	err := body.ReadCloser.Close()
	body.cancel()
	return err
}

func (service *service) filesDriveOpen(ctx context.Context, record fileRecord) (io.ReadCloser, error) {
	ctx, cancel := context.WithCancel(ctx)
	bearer, err := service.filesDriveAuthorization(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, "GET", service.filesDriveOrigin()+"/drive/v3/files/"+url.PathEscape(record.DriveID)+"?alt=media", nil)
	if err != nil {
		cancel()
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	response, err := service.client.Do(request)
	if err != nil {
		cancel()
		return nil, failure(502, "files_drive_unreachable")
	}
	if response.StatusCode != 200 || response.ContentLength >= 0 && response.ContentLength != record.File.SizeBytes {
		closeErr := response.Body.Close()
		cancel()
		return nil, errors.Join(failure(502, "files_drive_content_unavailable"), closeErr)
	}
	return &filesDriveBody{ReadCloser: response.Body, remaining: record.File.SizeBytes, cancel: cancel}, nil
}

// saveGeneratedFile stores bytes in Drive, not in the encrypted local catalog.
// A trusted generation receipt/output key gives retries one stable Files ID.
func (service *service) saveGeneratedFile(ctx context.Context, caller, key, mimeType string, content []byte) (fileResource, error) {
	store, err := service.filesStore(caller)
	if err != nil {
		return fileResource{}, err
	}
	if key == "" || len(key) > 4096 || len(content) == 0 || len(content) > filesMaxBytes {
		return fileResource{}, failure(413, "generated_file_size_or_key_rejected")
	}
	if !filesValidMIME(mimeType) {
		return fileResource{}, failure(400, "file_mime_type_invalid")
	}
	generatedKey := filesDigest("generated\x00" + caller + "\x00" + key)
	id := generatedKey[:32]
	release, err := service.fileLeases.acquire(ctx, filesMetadataName(caller, id))
	if err != nil {
		return fileResource{}, err
	}
	defer release()
	digest := sha256.Sum256(content)
	hash := base64.StdEncoding.EncodeToString(digest[:])
	record, err := store.filesReadRecord(caller, id)
	if err == nil {
		if record.GeneratedKey != generatedKey || record.File.Source != "GENERATED" || record.File.SHA256Hash != hash || record.File.MIMEType != mimeType || record.File.SizeBytes != int64(len(content)) || record.File.State == "DELETING" {
			return fileResource{}, failure(409, "generated_file_conflict")
		}
		if record.File.State == "ACTIVE" {
			return record.File, nil
		}
	} else {
		if !filesMissing(err) {
			return fileResource{}, err
		}
		now := service.now().UTC().Format(time.RFC3339Nano)
		record = fileRecord{GeneratedKey: generatedKey, File: fileResource{
			Name: "files/" + id, MIMEType: mimeType, SizeBytes: int64(len(content)), CreateTime: now, UpdateTime: now, SHA256Hash: hash,
			URI: "files/" + id, DownloadURI: "/v1beta/files/" + id + ":download?alt=media", State: "PROCESSING", Source: "GENERATED",
		}}
		record, err = service.filesCreateDriveRecord(ctx, store, caller, record)
		if err != nil {
			return fileResource{}, err
		}
	}
	if record.DriveURL == "" {
		record.DriveURL, err = service.filesDriveBegin(ctx, caller, record)
		if err != nil {
			return fileResource{}, err
		}
		if err := store.filesWriteRecord(caller, record); err != nil {
			return fileResource{}, err
		}
	}
	if !record.DriveComplete {
		record, err = service.filesDriveTransfer(ctx, record, nil, true)
		if err != nil {
			return fileResource{}, err
		}
		if err := store.filesWriteRecord(caller, record); err != nil {
			return fileResource{}, err
		}
	}
	for !record.DriveComplete {
		end := min(record.Received+filesChunkBytes, int64(len(content)))
		if end == record.Received {
			return fileResource{}, failure(502, "files_drive_upload_incomplete")
		}
		before := record.Received
		record, err = service.filesDriveTransfer(ctx, record, content[before:end], false)
		if err != nil {
			return fileResource{}, err
		}
		if record.Received != end {
			return fileResource{}, failure(502, "files_drive_offset_invalid")
		}
		if err := store.filesWriteRecord(caller, record); err != nil {
			return fileResource{}, err
		}
	}
	record.File.State, record.File.UpdateTime = "ACTIVE", service.now().UTC().Format(time.RFC3339Nano)
	if err := store.filesWriteRecord(caller, record); err != nil {
		return fileResource{}, err
	}
	return record.File, nil
}
