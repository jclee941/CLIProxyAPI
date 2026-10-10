package main

import (
	"context"
	"encoding/base64"
	"net/http"
)

type flowImageUploadOptions struct {
	Name         string            `json:"name"`
	Image        flowReferenceWire `json:"image"`
	CollectionID string            `json:"collectionId"`
	WorkflowID   string            `json:"workflowId"`
	Hidden       bool              `json:"hidden"`
}

func (service *service) uploadFlowImage(ctx context.Context, record storageRecord, project string, reference flowReference, options flowImageUploadOptions) (string, any, error) {
	var id string
	var uploaded any
	err := service.flowSubmit(ctx, record, project, "UPLOAD_IMAGE", func(token string) (string, any) {
		clientContext := flowContext(project, token)
		if options.WorkflowID != "" {
			clientContext[4] = options.WorkflowID
		}
		if options.CollectionID != "" {
			clientContext[7] = options.CollectionID
		}
		var crop any
		if coordinates := reference.CropCoordinates; coordinates != nil {
			crop = []any{coordinates.Top, coordinates.Left, coordinates.Bottom, coordinates.Right}
		}
		return "maseQ", []any{clientContext, base64.StdEncoding.EncodeToString(reference.data), reference.mimeType, 1, nil, nil, crop, options.Hidden, options.Name, nil, flowID(), flowID()}
	}, func(payload any) error {
		id, uploaded = flowUploadedMedia(payload, project), payload
		if id == "" {
			return failure(502, "flow_upload_missing")
		}
		return nil
	})
	return id, uploaded, err
}

func (service *service) flowUploadImageHTTP(ctx context.Context, record storageRecord, project string, request flowHTTPRequest) (httpResponse, error) {
	var input flowImageUploadOptions
	if err := flowStrict("image_upload", request.Body, &input); err != nil {
		return httpResponse{}, err
	}
	if !flowUploadName(input.Name) || input.Image.MediaID != "" || input.Image.InlineData == nil ||
		input.CollectionID != "" && !flowUUIDPattern.MatchString(input.CollectionID) ||
		input.WorkflowID != "" && !flowUUIDPattern.MatchString(input.WorkflowID) {
		return httpResponse{}, failure(400, "flow_image_upload_invalid")
	}
	if len(input.Image.InlineData.Data) > base64.StdEncoding.EncodedLen(20<<20) {
		return httpResponse{}, failure(413, "flow_image_upload_too_large")
	}
	reference, err := flowWireReference(input.Image)
	if err != nil {
		return httpResponse{}, err
	}
	switch reference.mimeType {
	case "image/png", "image/jpeg", "image/webp", "image/gif", "image/heif", "image/heic":
	default:
		return httpResponse{}, failure(400, "flow_image_upload_type_unsupported")
	}
	if len(reference.data) > 20<<20 {
		return httpResponse{}, failure(413, "flow_image_upload_too_large")
	}
	if _, err := service.getFlowProject(ctx, record, project); err != nil {
		return httpResponse{}, err
	}
	_, payload, err := service.uploadFlowImage(ctx, record, project, reference, input)
	if err != nil {
		return httpResponse{}, err
	}
	media, err := decodeFlowMedia(jsonField(payload, 0), project)
	if err != nil {
		return httpResponse{}, err
	}
	media.Title, _ = jsonField(payload, 1, 3, 0).(string)
	return flowJSON(http.StatusCreated, media)
}
