package main

import "context"

func (service *service) upscaleFlowVideo(ctx context.Context, record storageRecord, project string, input flowInput, selected flowSelected, media *flowGeneratedMedia) error {
	input.count = 1
	input.references = nil
	input.options = flowOptions{Mode: "upscale", SourceVideo: &flowVideoReference{MediaID: media.id}}
	selected.mode, selected.resolution, selected.usage = "upscale", selected.upscale, selected.upscaleUsage
	var operation flowOperation
	err := service.flowSubmit(ctx, record, project, "VIDEO_GENERATION", func(token string) (string, any) {
		return flowBatchArgs(project, token, input, selected)
	}, func(payload any) error {
		operation = flowVideoOperation(payload, project)
		if operation.id == "" {
			return failure(502, "flow_operation_missing")
		}
		return nil
	})
	if err != nil {
		return err
	}
	link, err := service.flowAwaitVideo(ctx, record, project, operation)
	if err != nil {
		return err
	}
	media.id, media.link = operation.media, link
	return nil
}
