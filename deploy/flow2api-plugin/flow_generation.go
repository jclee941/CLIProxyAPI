package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type flowGeneratedMedia struct {
	id, link string
	data     []byte
}

// A refused low-priority queue stays refused for hours (130 of 143 videos fell
// back on the night of 2026-10-10), and every refused try is three more
// submissions Flow counts against the account. After a refusal, low-priority
// requests on that account go straight to normal priority for this long.
const flowLowPriorityPause = 30 * time.Minute

func (service *service) lowPriorityPaused(account string) bool {
	service.flowMu.Lock()
	defer service.flowMu.Unlock()
	return service.now().Before(service.lowPriorityPausedUntil[account])
}

func (service *service) pauseLowPriority(account string) {
	service.flowMu.Lock()
	defer service.flowMu.Unlock()
	if service.lowPriorityPausedUntil == nil {
		service.lowPriorityPausedUntil = map[string]time.Time{}
	}
	service.lowPriorityPausedUntil[account] = service.now().Add(flowLowPriorityPause)
}

// A daily model quota Flow names stays spent until a reset the refusal does not
// state. From 2026-10-12 to 10-19 the image models answered 1,061 requests with
// PUBLIC_ERROR_PER_MODEL_DAILY_QUOTA_REACHED, 50 to 80 an hour from the evening
// until about 02:00 KST, and 113 of the 170 too-much-traffic refusals Veo got
// came within an hour of one. The web app stops offering a model once its daily
// quota is spent. After such a refusal the model is answered here on that
// account for this long; the first request after it goes to Flow and finds out
// whether the quota is back.
const flowDailyQuotaHold = 30 * time.Minute

// dailyQuotaHeld reports until when a daily quota refusal keeps the model on the
// account from being sent.
func (service *service) dailyQuotaHeld(account, model string) (time.Time, bool) {
	service.flowMu.Lock()
	defer service.flowMu.Unlock()
	until := service.dailyQuotaHeldUntil[account+"\x00"+model]
	return until, service.now().Before(until)
}

// holdDailyQuota starts the hold when Flow refused the model for its daily quota.
func (service *service) holdDailyQuota(account, model string, err error) {
	var public *publicError
	if !errors.As(err, &public) || public.Code != "flow_quota_exhausted" || !strings.Contains(public.Message, "PER_MODEL_DAILY_QUOTA_REACHED") {
		return
	}
	service.flowMu.Lock()
	defer service.flowMu.Unlock()
	if service.dailyQuotaHeldUntil == nil {
		service.dailyQuotaHeldUntil = map[string]time.Time{}
	}
	service.dailyQuotaHeldUntil[account+"\x00"+model] = service.now().Add(flowDailyQuotaHold)
}

// Below this balance a refused low-priority request is not sent at normal
// priority, which spends credits the caller did not ask for (5 for each
// 8-second 720p clip, about 900 on the night of 2026-10-10). The rest is kept
// for work the caller asks to run at normal priority.
const flowFallbackCreditFloor = 5000

// lowPriorityFallback refuses the normal-priority fallback below the credit
// floor, judged by the latest balance read.
func (service *service) lowPriorityFallback(account string) error {
	if reading, _ := service.lastFlowReading(account); reading.credits >= flowFallbackCreditFloor {
		return nil
	}
	message := fmt.Sprintf("flow_credit_floor_reached: Flow refused low priority, and normal priority is not used below %d credits", flowFallbackCreditFloor)
	return &publicError{Code: "flow_credit_floor_reached", Message: message, HTTPStatus: 429}
}

func (service *service) flowGenerate(ctx context.Context, record storageRecord, model flowModel, input flowInput) ([]byte, error) {
	if until, held := service.dailyQuotaHeld(record.SourceAuthID, model.id); held && input.options.Mode != "upscale" {
		message := fmt.Sprintf("flow_quota_exhausted: PUBLIC_ERROR_PER_MODEL_DAILY_QUOTA_REACHED; %s is not sent again before %s", model.id, until.UTC().Format(time.RFC3339))
		return nil, &publicError{Code: "flow_quota_exhausted", Message: message, HTTPStatus: 429}
	}
	if input.options.Priority == "low" && service.lowPriorityPaused(record.SourceAuthID) {
		if err := service.lowPriorityFallback(record.SourceAuthID); err != nil {
			return nil, err
		}
		service.report(map[string]any{"provider": provider, "state": "flow_low_priority_paused", "reason": model.id}, "flow2api: generation")
		input.options.Priority = "normal"
	}
	selected, err := service.selectFlowOptions(ctx, record, model, input)
	if err != nil {
		return nil, err
	}
	if source := input.options.SourceVideo; source != nil {
		err := service.withFlowSession(ctx, record, func(session *flowSession) error {
			media, err := session.rpc(ctx, "as29s", []any{source.MediaID}, flowProjectsPath, "")
			if err != nil {
				return err
			}
			if flowFindURL(media, "video") == "" {
				return failure(400, "flow_source_video_unavailable")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	project := input.options.ProjectID
	if project == "" {
		if project, err = service.flowProject(ctx, record); err != nil {
			return nil, err
		}
	}
	input.references = append(input.references, input.options.ReferenceImages...)
	input.options.ReferenceImages = nil
	if err := service.prepareFlowReferences(ctx, record, project, &input, model.video); err != nil {
		return nil, err
	}
	if input.options.Mode == "upscale" && !model.video {
		media := flowGeneratedMedia{id: input.options.BaseImage.MediaID}
		if err := service.upscaleFlowImage(ctx, record, project, input.imageSize, &media); err != nil {
			return nil, err
		}
		return service.flowMediaResults(ctx, model.id, project, []flowGeneratedMedia{media})
	}
	var media []flowGeneratedMedia
	var operations []flowOperation
	action := "IMAGE_GENERATION"
	if model.video {
		action = "VIDEO_GENERATION"
	}
	build := func(token string) (string, any) {
		return flowBatchArgs(project, token, input, selected)
	}
	accept := func(payload any) error {
		if model.video {
			flowVideoRecords(payload, func(value []any) {
				if value[1] == project {
					operations = append(operations, flowOperation{mediaID: value[0].(string), workflowID: value[2].(string)})
				}
			})
			if len(operations) == 0 {
				return failure(502, "flow_operation_missing")
			}
		} else {
			items, _ := jsonField(payload, 0).([]any)
			for _, item := range items {
				id, _ := jsonField(item, 0).(string)
				if link := flowFindURL(item, "image"); link != "" {
					media = append(media, flowGeneratedMedia{id: id, link: link})
				}
			}
			if len(media) == 0 {
				return failure(502, "flow_image_missing")
			}
		}
		return nil
	}
	err = service.flowSubmit(ctx, record, project, action, build, accept)
	if safeCredentialCode(err) == "flow_captcha_rejected" && input.options.Priority == "low" {
		// Flow refuses the free low-priority Veo Lite queue with UNUSUAL_ACTIVITY
		// once an account has leaned on it, while the same request at normal
		// priority is accepted. A refusal means nothing started, so the request
		// goes once more at normal priority, which spends credits, unless the
		// balance is below the credit floor.
		service.report(map[string]any{"provider": provider, "state": "flow_low_priority_refused", "reason": model.id}, "flow2api: generation")
		service.pauseLowPriority(record.SourceAuthID)
		if err = service.lowPriorityFallback(record.SourceAuthID); err == nil {
			input.options.Priority = "normal"
			if selected, err = service.selectFlowOptions(ctx, record, model, input); err == nil {
				err = service.flowSubmit(ctx, record, project, action, build, accept)
			}
		}
	}
	if err != nil {
		service.holdDailyQuota(record.SourceAuthID, model.id, err)
		return nil, err
	}
	// Every submitted operation is observed to its terminal state even if a
	// sibling fails. A batch is never replayed to repair partial results.
	var firstError error
	for _, operation := range operations {
		link, err := service.flowAwaitVideo(ctx, record, project, operation)
		if err != nil {
			if firstError == nil {
				firstError = err
			}
			continue
		}
		media = append(media, flowGeneratedMedia{id: operation.mediaID, link: link})
	}
	if firstError != nil {
		return nil, firstError
	}
	if len(media) != input.count {
		return nil, failure(502, "flow_generation_count_mismatch")
	}
	for index := range media {
		if !model.video && (input.imageSize == "2K" || input.imageSize == "4K") {
			if err := service.upscaleFlowImage(ctx, record, project, input.imageSize, &media[index]); err != nil {
				return nil, err
			}
		}
		if selected.upscale != 0 {
			if err := service.upscaleFlowVideo(ctx, record, project, input, selected, &media[index]); err != nil {
				return nil, err
			}
		}
	}
	return service.flowMediaResults(ctx, model.id, project, media)
}

func (service *service) prepareFlowReferences(ctx context.Context, record storageRecord, project string, input *flowInput, video bool) error {
	references := make([]*flowReference, 0, len(input.references)+3)
	for index := range input.references {
		references = append(references, &input.references[index])
	}
	for _, reference := range []*flowReference{input.options.FirstFrame, input.options.LastFrame, input.options.BaseImage} {
		if reference != nil {
			references = append(references, reference)
		}
	}
	for _, reference := range references {
		if reference.MediaID == "" {
			ids, err := service.flowUploads(ctx, record, project, []flowReference{*reference})
			if err != nil {
				return err
			}
			reference.MediaID = ids[0]
		}
		if !video && reference.CropCoordinates != nil {
			crop := reference.CropCoordinates
			err := service.withFlowSession(ctx, record, func(session *flowSession) error {
				payload, err := session.rpc(ctx, "sAVZzc", []any{1, reference.MediaID, nil,
					[]any{crop.Top, crop.Left, crop.Bottom, crop.Right}, nil, nil, flowID()}, "/project/"+project, "")
				if err != nil {
					return err
				}
				id, _ := jsonField(payload, 0, 0).(string)
				if id == "" {
					return failure(502, "flow_crop_result_missing")
				}
				for index := range input.options.StructuredPrompt {
					if input.options.StructuredPrompt[index].MediaID == reference.MediaID {
						input.options.StructuredPrompt[index].MediaID = id
					}
				}
				reference.MediaID, reference.CropCoordinates = id, nil
				return nil
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (service *service) upscaleFlowImage(ctx context.Context, record storageRecord, project, size string, media *flowGeneratedMedia) error {
	resolution := 1
	if size == "4K" {
		resolution = 2
	}
	return service.flowSubmit(ctx, record, project, "IMAGE_GENERATION", func(token string) (string, any) {
		return "SPrCad", []any{media.id, resolution, flowContext(project, token)}
	}, func(payload any) error {
		if link := flowFindURL(payload, "image"); link != "" {
			media.link = link
			return nil
		}
		if data := flowEncodedMedia(payload); data != nil {
			media.data = data
			return nil
		}
		return failure(502, "flow_upscale_missing")
	})
}

func (service *service) flowMediaResults(ctx context.Context, model, project string, media []flowGeneratedMedia) ([]byte, error) {
	candidates := make([]any, 0, len(media))
	for index, item := range media {
		if item.data == nil {
			data, err := service.flowDownload(ctx, item.link, 512*1024*1024)
			if err != nil {
				return nil, err
			}
			item.data = data
		}
		mime := http.DetectContentType(item.data)
		if !strings.HasPrefix(mime, "image/") && mime != "video/mp4" {
			return nil, failure(502, "flow_media_invalid")
		}
		candidates = append(candidates, map[string]any{
			"index": index, "finishReason": "STOP",
			"content": map[string]any{"role": "model", "parts": []any{map[string]any{
				"inlineData": map[string]any{"mimeType": mime, "data": base64.StdEncoding.EncodeToString(item.data)},
				"flow":       map[string]string{"mediaId": item.id, "projectId": project},
			}}},
		})
	}
	return json.Marshal(map[string]any{"modelVersion": model, "candidates": candidates})
}
