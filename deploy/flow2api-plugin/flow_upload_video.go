package main

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const flowUploadChunkBytes = 8 << 20

type flowUploadView struct {
	ID        string             `json:"id"`
	ProjectID string             `json:"projectId"`
	Status    string             `json:"status"`
	Offset    int64              `json:"offset"`
	Size      int64              `json:"sizeBytes"`
	ChunkSize int64              `json:"chunkSize"`
	Media     *flowMediaResource `json:"media,omitempty"`
}

func flowUploadName(name string) bool {
	return name != "" && utf8.ValidString(name) && utf8.RuneCountInString(name) <= 256 &&
		!strings.ContainsAny(name, "/\\") && !strings.ContainsFunc(name, unicode.IsControl)
}

func (service *service) startFlowVideoUpload(ctx context.Context, record storageRecord, project string, body []byte) (httpResponse, error) {
	var input struct {
		Name    string `json:"name"`
		MIME    string `json:"mimeType"`
		Size    int64  `json:"sizeBytes"`
		StartMS *int64 `json:"trimStartMilliseconds"`
		EndMS   *int64 `json:"trimEndMilliseconds"`
	}
	if err := flowStrict("upload", body, &input); err != nil {
		return httpResponse{}, err
	}
	mediaType, parameters, errMIME := mime.ParseMediaType(input.MIME)
	if !flowUploadName(input.Name) || errMIME != nil || len(parameters) != 0 || !strings.HasPrefix(mediaType, "video/") ||
		input.Size < 1 || input.Size > 1<<30 {
		return httpResponse{}, failure(400, "flow_video_upload_invalid")
	}
	input.MIME = mediaType
	if (input.StartMS == nil) != (input.EndMS == nil) ||
		input.StartMS != nil && (*input.StartMS < 0 || *input.EndMS <= *input.StartMS) {
		return httpResponse{}, failure(400, "flow_video_trim_invalid")
	}
	if _, err := service.getFlowProject(ctx, record, project); err != nil {
		return httpResponse{}, err
	}
	var claims flowUploadClaims
	err := service.withFlowSession(ctx, record, func(session *flowSession) error {
		if session.page.build == "" {
			if err := session.bootstrap(ctx, "/project/"+project); err != nil {
				return err
			}
		}
		headers := http.Header{
			"X-Goog-Upload-Protocol":              {"resumable"},
			"X-Goog-Upload-Command":               {"start"},
			"X-Goog-Upload-Header-Content-Length": {strconv.FormatInt(input.Size, 10)},
			"X-Goog-Upload-Header-Content-Type":   {input.MIME},
			"X-Framework-Xsrf-Token":              {session.page.xsrf},
			"Slug":                                {url.PathEscape(input.Name)},
			"Origin":                              {flowOrigin},
			"Referer":                             {flowOrigin + "/project/" + project},
		}
		if input.StartMS != nil {
			headers.Set("X-Trim-Start", strconv.FormatInt(*input.StartMS, 10))
			headers.Set("X-Trim-End", strconv.FormatInt(*input.EndMS, 10))
		}
		reply, err := service.exchange(ctx, record.SourceAuthID, "POST", session.origin+"/upload/v1/flow/upload/video/"+project, nil, headers)
		if err != nil {
			return err
		}
		if reply.StatusCode != 200 || reply.Headers.Get("X-Goog-Upload-Status") != "active" {
			return failure(502, "flow_upload_start_failed")
		}
		target := reply.Headers.Get("X-Goog-Upload-URL")
		if !flowUploadURL(target, session.origin, project) {
			return failure(502, "flow_upload_url_invalid")
		}
		granularity := int64(1)
		if raw := reply.Headers.Get("X-Goog-Upload-Chunk-Granularity"); raw != "" {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value < 1 || value > flowUploadChunkBytes {
				return failure(502, "flow_upload_granularity_invalid")
			}
			granularity = value
		}
		claims = flowUploadClaims{AccountID: record.SourceAuthID, ProjectID: project, URL: target, Size: input.Size, Granularity: granularity}
		return nil
	})
	if err != nil {
		return httpResponse{}, err
	}
	token, err := sealFlowUpload(claims)
	if err != nil {
		return httpResponse{}, err
	}
	return flowJSON(201, flowUploadView{ID: token, ProjectID: project, Status: "active", Size: claims.Size, ChunkSize: flowUploadChunkBytes / claims.Granularity * claims.Granularity})
}

func (service *service) flowVideoUploadHTTP(ctx context.Context, record storageRecord, token string, request flowHTTPRequest) (httpResponse, error) {
	claims, err := service.openFlowUpload(token, record.SourceAuthID)
	if err != nil {
		return httpResponse{}, err
	}
	command := "query"
	offset := int64(0)
	switch request.Method {
	case http.MethodGet:
		if len(request.Body) != 0 {
			return httpResponse{}, failure(400, "flow_upload_query_body_invalid")
		}
	case http.MethodDelete:
		command = "cancel"
	case http.MethodPost:
		offset, err = strconv.ParseInt(request.Query.Get("offset"), 10, 64)
		finalize := request.Query.Get("finalize")
		if err != nil || offset < 0 || offset > claims.Size || finalize != "" && finalize != "true" && finalize != "false" ||
			len(request.Body) > flowUploadChunkBytes || int64(len(request.Body)) > claims.Size-offset {
			return httpResponse{}, failure(400, "flow_upload_offset_or_size_invalid")
		}
		if offset%claims.Granularity != 0 || finalize != "true" && (len(request.Body) == 0 || int64(len(request.Body))%claims.Granularity != 0) ||
			finalize == "true" && offset+int64(len(request.Body)) != claims.Size {
			return httpResponse{}, failure(400, "flow_upload_chunk_invalid")
		}
		command = "upload"
		if finalize == "true" {
			command = "upload, finalize"
			if len(request.Body) == 0 {
				command = "finalize"
			}
		}
	default:
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	reply, err := service.exchange(ctx, record.SourceAuthID, "POST", claims.URL, request.Body, http.Header{
		"X-Goog-Upload-Command": {command}, "X-Goog-Upload-Offset": {strconv.FormatInt(offset, 10)},
		"Content-Type": {"application/octet-stream"}, "Origin": {flowOrigin},
	})
	if err != nil {
		if request.Method == http.MethodPost {
			switch safeCredentialCode(err) {
			case "flow_transport_failed", "flow_response_failed", "flow_response_invalid":
				return httpResponse{}, failure(502, "flow_upload_outcome_unknown")
			}
		}
		return httpResponse{}, err
	}
	if reply.StatusCode == 404 || reply.StatusCode == 410 {
		return httpResponse{}, failure(410, "flow_upload_expired")
	}
	if reply.StatusCode < 200 || reply.StatusCode >= 300 {
		if request.Method == http.MethodPost && reply.StatusCode >= 500 {
			return httpResponse{}, failure(502, "flow_upload_outcome_unknown")
		}
		return httpResponse{}, failure(502, "flow_upload_upstream_failed")
	}
	view := flowUploadView{ID: token, ProjectID: claims.ProjectID, Size: claims.Size, ChunkSize: flowUploadChunkBytes / claims.Granularity * claims.Granularity}
	if command == "cancel" {
		view.Status = "cancelled"
		return flowJSON(200, view)
	}
	switch reply.Headers.Get("X-Goog-Upload-Status") {
	case "active":
		view.Status = "active"
		if raw := reply.Headers.Get("X-Goog-Upload-Size-Received"); raw != "" {
			view.Offset, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || view.Offset < 0 || view.Offset > claims.Size {
				return httpResponse{}, failure(502, "flow_upload_offset_invalid")
			}
		} else if command == "query" {
			return httpResponse{}, failure(502, "flow_upload_offset_missing")
		} else {
			view.Offset = offset + int64(len(request.Body))
		}
	case "final":
		var uploaded struct {
			Media struct {
				Name, ProjectID string
			}
			Workflow struct {
				Name     string
				Metadata struct{ DisplayName string }
			}
		}
		if json.Unmarshal(reply.Body, &uploaded) != nil || !flowIdentifier(uploaded.Media.Name) || uploaded.Media.ProjectID != claims.ProjectID {
			return httpResponse{}, failure(502, "flow_upload_media_invalid")
		}
		view.Status, view.Offset = "complete", claims.Size
		title, err := url.PathUnescape(uploaded.Workflow.Metadata.DisplayName)
		if err != nil {
			return httpResponse{}, failure(502, "flow_upload_metadata_invalid")
		}
		view.Media = &flowMediaResource{ID: uploaded.Media.Name, ProjectID: claims.ProjectID, WorkflowID: uploaded.Workflow.Name, Title: title, Type: "video", MIMEType: "video/mp4"}
	default:
		return httpResponse{}, failure(502, "flow_upload_status_invalid")
	}
	return flowJSON(200, view)
}
