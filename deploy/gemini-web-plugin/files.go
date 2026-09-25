package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Matches the generic authenticated frontend_http RPC, not management.handle.
// CallerScope is supplied by CPA after authentication, never read from headers.
type filesHTTPRequest struct {
	Method      string
	Path        string
	RawPath     string
	RawQuery    string
	Scheme      string
	Host        string
	Headers     http.Header
	Query       url.Values
	Body        []byte
	CallerScope string `json:"caller_scope"`
}

func filesRegistration() json.RawMessage {
	return json.RawMessage(`{"Routes":[{"Method":"POST","Path":"/upload/v1beta/files"},{"Method":"POST","Path":"/upload/v1beta/files/resumable","AuthMode":"scoped"},{"Method":"GET","Path":"/v1beta/files"},{"Method":"GET","Path":"/v1beta/files/{id}"},{"Method":"DELETE","Path":"/v1beta/files/{id}"},{"Method":"GET","Path":"/v1beta/files/{id}:download"}]}`)
}

func filesJSON(value any) (httpResponse, error) {
	body, err := json.Marshal(value)
	return httpResponse{StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}, "Cache-Control": {"private, no-store"}}, Body: body}, err
}

func filesHTTPError(err error, upload bool) httpResponse {
	public := failure(500, "files_operation_failed")
	var known *publicError
	if errors.As(err, &known) {
		public = known
	}
	status := map[int]string{400: "INVALID_ARGUMENT", 401: "UNAUTHENTICATED", 404: "NOT_FOUND", 409: "FAILED_PRECONDITION", 410: "NOT_FOUND", 413: "RESOURCE_EXHAUSTED", 500: "INTERNAL", 502: "UNAVAILABLE", 503: "UNAVAILABLE"}[public.HTTPStatus]
	body, marshalErr := json.Marshal(map[string]any{"error": map[string]any{"code": public.HTTPStatus, "message": public.Code, "status": status}})
	if marshalErr != nil {
		return httpResponse{StatusCode: 500, Body: []byte(`{"error":{"code":500,"status":"INTERNAL"}}`)}
	}
	response := httpResponse{StatusCode: public.HTTPStatus, Headers: http.Header{"Content-Type": {"application/json"}, "Cache-Control": {"private, no-store"}}, Body: body}
	if upload {
		// google-genai retries responses without this header before checking their
		// HTTP status. Errors must terminate, not repeat a possibly committed PUT.
		response.Headers.Set("X-Goog-Upload-Status", "cancelled")
	}
	return response
}

func (service *service) filesHTTP(ctx context.Context, raw []byte) (httpResponse, error) {
	var request filesHTTPRequest
	if json.Unmarshal(raw, &request) != nil {
		return filesHTTPError(failure(400, "files_request_invalid"), false), nil
	}
	response, err := service.filesRoute(ctx, request)
	if err != nil {
		return filesHTTPError(err, request.Path == "/upload/v1beta/files" || request.Path == filesResumablePath), nil
	}
	return response, nil
}

func (service *service) filesRoute(ctx context.Context, request filesHTTPRequest) (httpResponse, error) {
	store, err := service.filesStore(request.CallerScope)
	if err != nil {
		return httpResponse{}, err
	}
	if len(request.Body) > filesChunkBytes {
		return httpResponse{}, failure(413, "file_chunk_too_large")
	}
	if request.Method == "POST" && request.Path == "/upload/v1beta/files" {
		if _, found := request.Query["upload_id"]; found {
			return httpResponse{}, failure(400, "file_upload_start_invalid")
		}
		return service.filesStart(ctx, store, request)
	}
	if request.Path == filesResumablePath {
		return service.filesUpload(ctx, store, request)
	}
	if request.Method == "GET" && request.Path == "/v1beta/files" {
		return service.filesList(store, request)
	}
	reference, found := strings.CutPrefix(request.Path, "/v1beta/")
	download := strings.HasSuffix(reference, ":download")
	if download {
		reference = strings.TrimSuffix(reference, ":download")
	}
	id, valid := filesReferenceID(reference)
	if !found || !valid || request.Method != "GET" && request.Method != "DELETE" || download && request.Method != "GET" {
		return httpResponse{}, failure(404, "files_route_not_found")
	}
	if request.Method == "DELETE" {
		if err := service.filesDelete(ctx, store, request.CallerScope, id); err != nil {
			return httpResponse{}, err
		}
		return filesJSON(struct{}{})
	}
	record, err := store.filesReadRecord(request.CallerScope, id)
	if err != nil {
		return httpResponse{}, err
	}
	if record.File.State == "DELETING" {
		return httpResponse{}, failure(404, "file_not_found")
	}
	if !download {
		return filesJSON(record.File)
	}
	if record.File.Source != "GENERATED" {
		return httpResponse{}, failure(400, "only_generated_files_can_be_downloaded")
	}
	if record.File.State != "ACTIVE" || request.Query.Get("alt") != "media" {
		return httpResponse{}, failure(400, "file_download_requires_active_media")
	}
	reader, err := service.filesDriveOpen(ctx, record)
	if err != nil {
		return httpResponse{}, err
	}
	body, readErr := io.ReadAll(reader)
	if err := errors.Join(readErr, reader.Close()); err != nil {
		return httpResponse{}, err
	}
	return httpResponse{StatusCode: 200, Headers: http.Header{
		"Content-Type": {record.File.MIMEType}, "Content-Length": {strconv.Itoa(len(body))}, "Cache-Control": {"private, no-store"}, "X-Content-Type-Options": {"nosniff"},
	}, Body: body}, nil
}

func filesValidMIME(value string) bool {
	if len(value) > 255 || strings.ContainsAny(value, "\r\n") {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && strings.Contains(mediaType, "/") && !strings.Contains(mediaType, "*")
}

func filesStartMetadata(request filesHTTPRequest) (fileResource, error) {
	var body struct {
		File struct {
			Name         string          `json:"name"`
			DisplayName  string          `json:"displayName"`
			DisplaySnake string          `json:"display_name"`
			MIMEType     string          `json:"mimeType"`
			MIMESnake    string          `json:"mime_type"`
			SizeBytes    json.RawMessage `json:"sizeBytes"`
			SizeSnake    json.RawMessage `json:"size_bytes"`
		} `json:"file"`
	}
	if len(request.Body) > 64*1024 || strictJSON(request.Body, &body) != nil || request.Headers.Get("X-Goog-Upload-Protocol") != "resumable" || request.Headers.Get("X-Goog-Upload-Command") != "start" {
		return fileResource{}, failure(400, "file_upload_start_invalid")
	}
	size, err := strconv.ParseInt(request.Headers.Get("X-Goog-Upload-Header-Content-Length"), 10, 64)
	if err != nil || size < 0 {
		return fileResource{}, failure(400, "file_size_invalid")
	}
	if size > filesMaxBytes {
		return fileResource{}, failure(413, "file_exceeds_100_mib_limit")
	}
	mimeType := request.Headers.Get("X-Goog-Upload-Header-Content-Type")
	if !filesValidMIME(mimeType) {
		return fileResource{}, failure(400, "file_mime_type_invalid")
	}
	for _, value := range []string{body.File.MIMEType, body.File.MIMESnake} {
		if value != "" && value != mimeType {
			return fileResource{}, failure(400, "file_mime_type_mismatch")
		}
	}
	for _, value := range []json.RawMessage{body.File.SizeBytes, body.File.SizeSnake} {
		if len(value) == 0 || string(value) == "null" {
			continue
		}
		text := string(value)
		if value[0] == '"' && json.Unmarshal(value, &text) != nil {
			return fileResource{}, failure(400, "file_size_invalid")
		}
		declared, err := strconv.ParseInt(text, 10, 64)
		if err != nil || declared != size {
			return fileResource{}, failure(400, "file_size_mismatch")
		}
	}
	name := body.File.Name
	if name == "" {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return fileResource{}, failure(500, "file_id_failed")
		}
		name = "files/" + hex.EncodeToString(random[:])
	}
	if _, valid := filesReferenceID(name); !valid {
		return fileResource{}, failure(400, "file_name_invalid")
	}
	display := body.File.DisplayName
	if display == "" {
		display = body.File.DisplaySnake
	} else if body.File.DisplaySnake != "" && body.File.DisplaySnake != display {
		return fileResource{}, failure(400, "file_display_name_invalid")
	}
	if display == "" {
		display = request.Headers.Get("X-Goog-Upload-File-Name")
	}
	if !utf8.ValidString(display) || utf8.RuneCountInString(display) > 512 {
		return fileResource{}, failure(400, "file_display_name_invalid")
	}
	return fileResource{Name: name, DisplayName: display, MIMEType: mimeType, SizeBytes: size, URI: name, State: "PROCESSING", Source: "UPLOADED"}, nil
}

func (service *service) filesStart(ctx context.Context, store *sessionStore, request filesHTTPRequest) (httpResponse, error) {
	resource, err := filesStartMetadata(request)
	if err != nil {
		return httpResponse{}, err
	}
	origin, err := url.Parse(request.Scheme + "://" + request.Host)
	if err != nil || request.Scheme != "http" && request.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return httpResponse{}, failure(400, "file_request_origin_invalid")
	}
	// The configured public origin restores HTTPS behind the operator's proxy.
	// Arbitrary forwarded headers cannot change the upload credential destination.
	if public, err := url.Parse(service.settings().ManagerOrigin); err == nil &&
		public.Scheme == "https" && public.Host == origin.Host {
		origin.Scheme = "https"
	}
	id, _ := filesReferenceID(resource.Name)
	release, err := service.fileLeases.acquire(ctx, filesMetadataName(request.CallerScope, id))
	if err != nil {
		return httpResponse{}, err
	}
	defer release()
	if _, err := store.filesReadRecord(request.CallerScope, id); err == nil {
		return httpResponse{}, failure(409, "file_already_exists")
	} else if !filesMissing(err) {
		return httpResponse{}, err
	}
	store.mu.Lock()
	token, err := store.filesNewUploadLocked(request.CallerScope, id, service.now())
	store.mu.Unlock()
	if err != nil {
		return httpResponse{}, err
	}
	now := service.now().UTC().Format(time.RFC3339Nano)
	resource.CreateTime, resource.UpdateTime = now, now
	_, err = service.filesCreateDriveRecord(ctx, store, request.CallerScope, fileRecord{File: resource, UploadID: token})
	if err != nil {
		return httpResponse{}, err
	}
	origin.Path, origin.RawQuery = filesResumablePath, url.Values{"upload_id": {token}}.Encode()
	response, err := filesJSON(struct{}{})
	response.Headers.Set("X-Goog-Upload-URL", origin.String())
	response.Headers.Set("X-Goog-Upload-Status", "active")
	response.Headers.Set("X-Goog-Upload-Chunk-Granularity", strconv.Itoa(filesGranularity))
	response.Headers.Set("X-Goog-Upload-Size-Received", "0")
	return response, err
}

func filesUploadResponse(record fileRecord) (httpResponse, error) {
	var value any = struct{}{}
	status := "active"
	if record.File.State == "ACTIVE" {
		status = "final"
		value = map[string]any{"file": record.File}
	}
	response, err := filesJSON(value)
	response.Headers.Set("X-Goog-Upload-Status", status)
	response.Headers.Set("X-Goog-Upload-Size-Received", strconv.FormatInt(record.Received, 10))
	return response, err
}

func (service *service) filesUpload(ctx context.Context, store *sessionStore, request filesHTTPRequest) (httpResponse, error) {
	token, err := filesUploadToken(filesUploadAuthRequest{Method: request.Method, Path: request.Path, RawPath: request.RawPath, RawQuery: request.RawQuery, Query: request.Query})
	if err != nil {
		return httpResponse{}, err
	}
	store.mu.Lock()
	claims, err := store.filesUploadClaimsLocked(token, service.now())
	store.mu.Unlock()
	if err != nil {
		return httpResponse{}, err
	}
	if claims.CallerScope != request.CallerScope {
		return httpResponse{}, failure(401, "file_upload_capability_invalid")
	}
	command := strings.ReplaceAll(request.Headers.Get("X-Goog-Upload-Command"), " ", "")
	if command != "upload" && command != "upload,finalize" && command != "finalize" && command != "query" {
		return httpResponse{}, failure(400, "file_upload_command_invalid")
	}
	if (command == "query" || command == "finalize") && len(request.Body) != 0 {
		return httpResponse{}, failure(400, "file_upload_command_body_invalid")
	}
	release, err := service.fileLeases.acquire(ctx, filesMetadataName(request.CallerScope, claims.FileID))
	if err != nil {
		return httpResponse{}, err
	}
	defer release()
	// Authentication may precede a competing delete or a wait on this lease.
	// Recheck expiry, ownership and the exact persisted UploadID while leased.
	store.mu.Lock()
	claims, record, err := store.filesUploadCapabilityLocked(token, service.now())
	store.mu.Unlock()
	if err != nil {
		return httpResponse{}, err
	}
	if claims.CallerScope != request.CallerScope {
		return httpResponse{}, failure(401, "file_upload_capability_invalid")
	}
	if record.File.State == "ACTIVE" {
		if command != "query" {
			return httpResponse{}, failure(409, "file_upload_already_finalized")
		}
		return filesUploadResponse(record)
	}
	// Drive is the authority for accepted bytes. Query before each mutation so a
	// lost PUT response or process restart never permits a duplicate chunk.
	if !record.DriveComplete {
		record, err = service.filesDriveTransfer(ctx, record, nil, true)
		if err != nil {
			return httpResponse{}, err
		}
		if record.DriveComplete && record.FinalizeRequested {
			record.File.State = "ACTIVE"
		}
		if err := store.filesWriteRecord(request.CallerScope, record); err != nil {
			return httpResponse{}, err
		}
	}
	if command == "query" || record.File.State == "ACTIVE" {
		if command != "query" {
			return httpResponse{}, failure(409, "file_upload_already_finalized")
		}
		return filesUploadResponse(record)
	}
	offset, err := strconv.ParseInt(request.Headers.Get("X-Goog-Upload-Offset"), 10, 64)
	if err != nil || offset != record.Received {
		return httpResponse{}, failure(400, "file_upload_offset_mismatch")
	}
	finalize := command == "finalize" || command == "upload,finalize"
	end := offset + int64(len(request.Body))
	if end > record.File.SizeBytes || finalize && end != record.File.SizeBytes || !finalize && (len(request.Body) == 0 || end < record.File.SizeBytes && len(request.Body)%filesGranularity != 0) {
		return httpResponse{}, failure(400, "file_upload_chunk_size_invalid")
	}
	if finalize {
		record.FinalizeRequested = true
		if err := store.filesWriteRecord(request.CallerScope, record); err != nil {
			return httpResponse{}, err
		}
	}
	if len(request.Body) > 0 || !record.DriveComplete && record.File.SizeBytes == 0 {
		record, err = service.filesDriveTransfer(ctx, record, request.Body, false)
		if err != nil {
			return httpResponse{}, err
		}
		if record.Received != end {
			return httpResponse{}, failure(502, "files_drive_offset_invalid")
		}
	}
	if finalize {
		if !record.DriveComplete {
			return httpResponse{}, failure(502, "files_drive_upload_incomplete")
		}
		record.File.State = "ACTIVE"
	}
	record.File.UpdateTime = service.now().UTC().Format(time.RFC3339Nano)
	if err := store.filesWriteRecord(request.CallerScope, record); err != nil {
		return httpResponse{}, err
	}
	return filesUploadResponse(record)
}

func (service *service) filesList(store *sessionStore, request filesHTTPRequest) (httpResponse, error) {
	size := 10
	if value := request.Query.Get("pageSize"); value != "" {
		var err error
		size, err = strconv.Atoi(value)
		if err != nil || size < 1 || size > 100 {
			return httpResponse{}, failure(400, "files_page_size_invalid")
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.filesReadyLocked(); err != nil {
		return httpResponse{}, err
	}
	after := ""
	if token := request.Query.Get("pageToken"); token != "" {
		var err error
		after, err = store.filesParseTokenLocked(request.CallerScope, "page", token)
		if err != nil {
			return httpResponse{}, failure(400, "files_page_token_invalid")
		}
	}
	all, err := store.filesListLocked(request.CallerScope)
	if err != nil {
		return httpResponse{}, err
	}
	page := struct {
		Files         []fileResource `json:"files"`
		NextPageToken string         `json:"nextPageToken,omitempty"`
	}{Files: make([]fileResource, 0)}
	for _, file := range all {
		if file.Name <= after {
			continue
		}
		if len(page.Files) == size {
			page.NextPageToken, err = store.filesTokenLocked(request.CallerScope, "page", page.Files[len(page.Files)-1].Name)
			if err != nil {
				return httpResponse{}, err
			}
			break
		}
		page.Files = append(page.Files, file)
	}
	return filesJSON(page)
}
