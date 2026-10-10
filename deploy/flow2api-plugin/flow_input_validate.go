package main

import (
	"slices"
	"strings"
)

func flowDefaults(model flowModel, input *flowInput) error {
	upscale := input.options.Mode == flowModeUpscale
	if input.count == 0 {
		input.count = 1
	}
	if upscale && input.count != 1 {
		return failure(400, "flow_invalid_candidate_count")
	}
	if input.seed != nil && (model.video && !upscale || !model.video && upscale) {
		return failure(400, "flow_seed_unsupported")
	}
	if upscale && input.seconds != 0 {
		return flowOptionRejection("durationSeconds")
	}
	if model.video {
		// Video framing defaults to portrait, matching the Gemini video path.
		if input.aspect == "" {
			input.aspect = "9:16"
		}
		if input.aspect != "9:16" && input.aspect != "16:9" {
			return failure(400, "flow_invalid_aspect_ratio")
		}
		if input.seconds == 0 {
			input.seconds = 8
		}
		allowed := []int{4, 6, 8}
		if model.family == "omni" {
			allowed = append(allowed, 10)
		}
		if !slices.Contains(allowed, input.seconds) {
			return failure(400, "flow_invalid_duration")
		}
		switch {
		case input.resolution != "":
		case upscale:
			// An upscale to an unnamed resolution would be a guess.
			return failure(400, "flow_invalid_resolution")
		default:
			input.resolution = "720p"
		}
		return flowRequirePrompt(input)
	}
	if input.aspect == "" {
		input.aspect = "1:1"
	}
	if flowImageAspect(input.aspect) == 0 {
		return failure(400, "flow_invalid_aspect_ratio")
	}
	switch input.imageSize {
	case "2K", "4K":
	case "", "1K":
		if upscale {
			return failure(400, "flow_invalid_image_size")
		}
	default:
		return failure(400, "flow_invalid_image_size")
	}
	return flowRequirePrompt(input)
}

// flowImageAspect is Flow's aspect enum for the five ratios the live web app
// names (1 square, 2 9:16, 3 16:9, 4 3:4, 5 4:3).
func flowImageAspect(aspect string) int {
	return map[string]int{"1:1": 1, "9:16": 2, "16:9": 3, "3:4": 4, "4:3": 5}[aspect]
}

func flowResolveOptions(model flowModel, input *flowInput) error {
	options := &input.options
	if options.Priority == "" {
		options.Priority = "normal"
	}
	if !model.video {
		if options.Priority != "normal" {
			return flowOptionRejection("flow.priority")
		}
		if options.Destination != nil && options.Destination.SceneID != "" {
			return flowOptionRejection("flow.destination.sceneId")
		}
		if slices.ContainsFunc(options.StructuredPrompt, func(part flowPromptPart) bool { return part.AudioID != "" }) {
			return flowOptionRejection("flow.structuredPrompt.audioId")
		}
	}
	structuredReferences := slices.ContainsFunc(options.StructuredPrompt, flowPromptPart.isReference)
	material := len(input.references)+len(options.ReferenceImages)+len(options.ReferenceAudio)+len(options.ReferenceLikenesses)+len(options.ReferenceEntities) > 0
	frames := options.FirstFrame != nil || options.LastFrame != nil
	base, source := options.BaseImage != nil, options.SourceVideo != nil
	if !model.video && len(input.references)+len(options.ReferenceImages)+btoi(base) > flowMaxImageReferences {
		return failure(400, "flow_too_many_references")
	}
	mode := options.Mode
	if mode == "" || mode == flowModeAuto {
		switch {
		case source:
			return failure(400, "flow_mode_required")
		case frames:
			mode = flowModeFrames
		case base:
			mode = flowModeEdit
		case material:
			mode = flowModeReferences
		default:
			mode = flowModeText
		}
	}
	options.Mode = mode
	conflict, missing := failure(400, "flow_conflicting_inputs"), failure(400, "flow_input_required")
	switch mode {
	case flowModeText:
		if material || frames || base || source || structuredReferences {
			return conflict
		}
	case flowModeReferences:
		if frames || base || source {
			return conflict
		}
		if !material {
			return missing
		}
	case flowModeFrames:
		switch {
		case !model.video:
			return failure(400, "flow_mode_unsupported")
		case options.FirstFrame == nil:
			return missing
		case material || base || source:
			return conflict
		}
	case flowModeEdit:
		if model.video && !source || !model.video && !base {
			return missing
		}
		if frames || base && model.video || source && !model.video {
			return conflict
		}
	case flowModeExtend:
		switch {
		case !model.video:
			return failure(400, "flow_mode_unsupported")
		case !source:
			return missing
		case frames || base:
			return conflict
		}
	case flowModeUpscale:
		if model.video && !source || !model.video && !base {
			return missing
		}
		if material || frames || structuredReferences || len(options.StructuredPrompt) > 0 {
			return conflict
		}
		if !model.video && (options.BaseImage.MediaID == "" || options.BaseImage.CropCoordinates != nil) {
			return failure(400, "flow_invalid_reference")
		}
		if source && (options.SourceVideo.StartFrame != nil || options.SourceVideo.EndFrame != nil) {
			return flowOptionRejection("flow.sourceVideo")
		}
		if options.Priority != "normal" {
			return flowOptionRejection("flow.priority")
		}
		if !model.video && options.Destination != nil {
			return flowOptionRejection("flow.destination")
		}
	}
	return nil
}

func btoi(value bool) int {
	if value {
		return 1
	}
	return 0
}

func flowRequirePrompt(input *flowInput) error {
	if input.options.Mode == flowModeUpscale {
		return nil
	}
	structured := false
	for _, part := range input.options.StructuredPrompt {
		if strings.TrimSpace(part.Text) != "" {
			structured = true
		}
	}
	switch {
	case structured && input.prompt != "":
		return failure(400, "flow_prompt_conflict")
	case structured || input.prompt != "":
		return nil
	}
	return failure(400, "flow_prompt_required")
}
