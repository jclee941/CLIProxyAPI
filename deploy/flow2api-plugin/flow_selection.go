package main

import "context"

type flowSelected struct {
	usage              flowModelUsage
	upscaleUsage       flowModelUsage
	mode               string
	aspect, resolution int
	upscale            int
}

func flowResolutionValue(value string) int {
	return map[string]int{"720p": 1, "1080p": 2, "4K": 3, "360p": 4}[value]
}

func (service *service) selectFlowOptions(ctx context.Context, record storageRecord, model flowModel, input flowInput) (flowSelected, error) {
	options := input.options
	images := len(input.references) + len(options.ReferenceImages)
	identities := len(options.ReferenceEntities) + len(options.ReferenceLikenesses)
	mode := options.Mode
	if mode == "" || mode == "auto" {
		switch {
		case options.SourceVideo != nil:
			mode = "edit"
		case options.FirstFrame != nil:
			mode = "frames"
		case images+identities+len(options.ReferenceAudio) > 0:
			mode = "references"
		default:
			mode = "text"
		}
	}
	selection := flowModelSelection{
		video: model.video, key: options.ModelKey, duration: input.seconds,
		images: images, identities: identities, audio: len(options.ReferenceAudio),
		inputs: []int{1},
	}
	selected := flowSelected{mode: mode}
	if !model.video {
		selection.family = map[string]string{"BELUGA": "beluga_display", "GEM_PIX_2": "nano_banana_pro", "HARBOR_SEAL": "harbor_seal"}[model.imageModel]
		selection.aspect = flowImageAspect(input.aspect)
		if mode == "upscale" {
			if input.imageSize == "2K" {
				selection.family, selection.inputs = "upsample_2k", []int{5}
			} else if input.imageSize == "4K" {
				selection.family, selection.inputs = "upsample_4k", []int{6}
			} else {
				return selected, failure(400, "flow_invalid_image_size")
			}
		} else {
			if images+identities > 0 {
				selection.inputs = append(selection.inputs, 3)
			}
			if options.BaseImage != nil {
				selection.inputs = append(selection.inputs, 4)
			}
			if identities > 0 {
				selection.inputs = append(selection.inputs, 7)
			}
		}
	} else {
		selection.family = map[string]string{"omni": "abra", "fast": "veo_3_1_fast", "quality": "veo_3_1_quality", "lite": "veo_3_1_lite"}[model.family]
		if options.Priority == "low" {
			if model.family != "lite" {
				return selected, failure(400, "flow_priority_unsupported")
			}
			selection.family = "veo_3_1_lite_low_priority"
		}
		selection.aspect = 2
		if input.aspect == "9:16" {
			selection.aspect = 1
		}
		selection.resolution = flowResolutionValue(string(input.resolution))
		if selection.resolution == 0 {
			selection.resolution = 1
		}
		switch mode {
		case "frames":
			selection.inputs = append(selection.inputs, 5)
			if options.LastFrame != nil {
				selection.inputs = append(selection.inputs, 7)
			}
		case "references", "edit":
			selection.inputs = append(selection.inputs, 6)
			if len(options.ReferenceAudio) > 0 {
				selection.inputs = append(selection.inputs, 18)
			}
			if identities > 0 {
				selection.inputs = append(selection.inputs, 19)
			}
			if mode == "edit" {
				selection.inputs = append(selection.inputs, 20)
			}
		case "extend":
			selection.inputs = append(selection.inputs, 14)
			if images > 0 {
				selection.inputs = append(selection.inputs, 6)
			}
			if len(options.ReferenceAudio) > 0 {
				selection.inputs = append(selection.inputs, 18)
			}
			if identities > 0 {
				selection.inputs = append(selection.inputs, 19)
			}
		case "upscale":
			switch selection.resolution {
			case 1:
				selection.family, selection.inputs = "omni_upsampler_360p", []int{21}
			case 2:
				selection.family, selection.inputs = "veo_3_1_upsampler_1080p", []int{15}
			case 3:
				selection.family, selection.inputs = "veo_3_1_upsampler_4k", []int{16}
			default:
				return selected, failure(400, "flow_invalid_resolution")
			}
		case "text":
		default:
			return selected, failure(400, "flow_invalid_mode")
		}
		if mode != "upscale" && (selection.resolution == 2 || selection.resolution == 3) {
			selected.upscale, selection.resolution = selection.resolution, 1
		}
	}
	var usages []flowModelUsage
	err := service.withFlowSession(ctx, record, func(session *flowSession) error {
		credits, err := session.rpc(ctx, "nzlxg", []any{}, flowProjectsPath, "")
		if err != nil {
			return err
		}
		var valid bool
		if selection.tier, valid = jsonInteger(jsonField(credits, 3)); !valid || selection.tier < 1 || selection.tier > 3 {
			return failure(502, "flow_account_tier_invalid")
		}
		catalog, err := session.rpc(ctx, "HTrJv", []any{}, flowProjectsPath, "")
		if err != nil {
			return err
		}
		usages, err = decodeFlowModels(catalog)
		return err
	})
	if err != nil {
		return selected, err
	}
	selected.usage, err = selectFlowModel(usages, selection)
	selected.aspect, selected.resolution = selection.aspect, selection.resolution
	if err == nil && selected.upscale != 0 {
		selection.family, selection.inputs = "veo_3_1_upsampler_1080p", []int{15}
		if selected.upscale == 3 {
			selection.family, selection.inputs = "veo_3_1_upsampler_4k", []int{16}
		}
		selection.key, selection.resolution = "", selected.upscale
		selection.images, selection.identities, selection.audio = 0, 0, 0
		selected.upscaleUsage, err = selectFlowModel(usages, selection)
	}
	if err == nil && !model.video && mode != "upscale" && (input.imageSize == "2K" || input.imageSize == "4K") {
		selection.family, selection.inputs = "upsample_2k", []int{5}
		if input.imageSize == "4K" {
			selection.family, selection.inputs = "upsample_4k", []int{6}
		}
		selection.key = ""
		selection.images, selection.identities = 0, 0
		_, err = selectFlowModel(usages, selection)
	}
	return selected, err
}
