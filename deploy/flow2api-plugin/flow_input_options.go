package main

import (
	"slices"
	"strings"
)

func parseFlowOptions(model flowModel, raw []byte, input *flowInput) error {
	var wire flowOptionsWire
	if err := flowStrict("flow", raw, &wire); err != nil {
		return err
	}
	if !model.video {
		for _, option := range []struct {
			key     string
			present bool
		}{
			{"flow.firstFrame", wire.FirstFrame != nil},
			{"flow.lastFrame", wire.LastFrame != nil},
			{"flow.sourceVideo", wire.SourceVideo != nil},
			{"flow.referenceAudio", len(wire.ReferenceAudio) > 0},
			{"flow.audioFailurePreference", wire.AudioFailurePreference != ""},
		} {
			if option.present {
				return flowOptionRejection(option.key)
			}
		}
	} else if wire.BaseImage != nil {
		return flowOptionRejection("flow.baseImage")
	}
	options := &input.options
	if !slices.Contains([]string{"", flowModeAuto, flowModeText, flowModeReferences, flowModeFrames, flowModeEdit, flowModeExtend, flowModeUpscale}, wire.Mode) {
		return failure(400, "flow_invalid_mode")
	}
	if !slices.Contains([]string{"", "normal", "low"}, wire.Priority) {
		return failure(400, "flow_invalid_priority")
	}
	if !slices.Contains([]string{"", "allow_silent", "require_audio"}, wire.AudioFailurePreference) {
		return failure(400, "flow_invalid_audio_preference")
	}
	for _, id := range []string{wire.ProjectID, wire.ModelKey} {
		if id != "" && !flowIdentifier(id) {
			return failure(400, "flow_invalid_identifier")
		}
	}
	options.Mode, options.Priority, options.ProjectID, options.ModelKey = wire.Mode, wire.Priority, wire.ProjectID, wire.ModelKey
	options.AudioFailurePreference = wire.AudioFailurePreference
	var err error
	for _, slot := range []struct {
		wire   *flowReferenceWire
		target **flowReference
	}{{wire.FirstFrame, &options.FirstFrame}, {wire.LastFrame, &options.LastFrame}, {wire.BaseImage, &options.BaseImage}} {
		if slot.wire == nil {
			continue
		}
		reference, err := flowWireReference(*slot.wire)
		if err != nil {
			return err
		}
		*slot.target = &reference
	}
	for _, item := range wire.ReferenceImages {
		reference, err := flowWireReference(item)
		if err != nil {
			return err
		}
		options.ReferenceImages = append(options.ReferenceImages, reference)
	}
	if source := wire.SourceVideo; source != nil {
		if !flowIdentifier(source.MediaID) || source.StartFrame != nil && *source.StartFrame < 0 || source.EndFrame != nil && *source.EndFrame < 0 ||
			source.StartFrame != nil && source.EndFrame != nil && *source.StartFrame >= *source.EndFrame {
			return failure(400, "flow_invalid_source_video")
		}
		options.SourceVideo = &flowVideoReference{MediaID: source.MediaID, StartFrame: source.StartFrame, EndFrame: source.EndFrame}
	}
	if options.ReferenceAudio, err = flowIdentifiers(wire.ReferenceAudio); err != nil {
		return err
	}
	if options.ReferenceLikenesses, err = flowIdentifiers(wire.ReferenceLikenesses); err != nil {
		return err
	}
	if options.ReferenceEntities, err = flowIdentifiers(wire.ReferenceEntities); err != nil {
		return err
	}
	if destination := wire.Destination; destination != nil {
		for _, id := range []string{destination.WorkflowID, destination.CollectionID, destination.SceneID} {
			if id != "" && !flowIdentifier(id) {
				return failure(400, "flow_invalid_destination")
			}
		}
		empty := destination.WorkflowID == "" && destination.CollectionID == "" && destination.SceneID == "" && destination.Position == nil
		if empty || destination.Position != nil && (*destination.Position < 0 || destination.SceneID == "") {
			return failure(400, "flow_invalid_destination")
		}
		options.Destination = &flowDestination{WorkflowID: destination.WorkflowID, CollectionID: destination.CollectionID, SceneID: destination.SceneID, Position: destination.Position}
	}
	if wire.StructuredPrompt != nil {
		if len(wire.StructuredPrompt.Parts) == 0 {
			return failure(400, "flow_invalid_structured_prompt")
		}
		for _, item := range wire.StructuredPrompt.Parts {
			var part flowPromptPart
			switch {
			case item.Text != nil && item.Reference == nil && *item.Text != "":
				part.Text = *item.Text
			case item.Text == nil && item.Reference != nil:
				reference := item.Reference
				part = flowPromptPart{MediaID: reference.MediaID, LikenessID: reference.LikenessID, EntityID: reference.EntityID, AudioID: reference.AudioID}
				chosen := 0
				for _, id := range []string{part.MediaID, part.LikenessID, part.EntityID, part.AudioID} {
					if id != "" {
						chosen++
						if !flowIdentifier(id) {
							return failure(400, "flow_invalid_structured_prompt")
						}
					}
				}
				if chosen != 1 {
					return failure(400, "flow_invalid_structured_prompt")
				}
			default:
				return failure(400, "flow_invalid_structured_prompt")
			}
			options.StructuredPrompt = append(options.StructuredPrompt, part)
		}
	}
	return nil
}

func flowWireReference(wire flowReferenceWire) (flowReference, error) {
	var reference flowReference
	switch {
	case wire.MediaID != "" && wire.InlineData == nil:
		if !flowIdentifier(wire.MediaID) {
			return flowReference{}, failure(400, "flow_invalid_reference")
		}
		reference.MediaID = wire.MediaID
	case wire.MediaID == "" && wire.InlineData != nil:
		var err error
		if reference, err = flowInlineReference(wire.InlineData.MimeType, wire.InlineData.Data); err != nil {
			return flowReference{}, err
		}
	default:
		return flowReference{}, failure(400, "flow_invalid_reference")
	}
	if crop := wire.CropCoordinates; crop != nil {
		if crop.Top == nil || crop.Left == nil || crop.Bottom == nil || crop.Right == nil {
			return flowReference{}, failure(400, "flow_invalid_crop")
		}
		value := flowCrop{Top: *crop.Top, Left: *crop.Left, Bottom: *crop.Bottom, Right: *crop.Right}
		for _, edge := range []float64{value.Top, value.Left, value.Bottom, value.Right} {
			if edge < 0 || edge > 1 {
				return flowReference{}, failure(400, "flow_invalid_crop")
			}
		}
		if value.Top >= value.Bottom || value.Left >= value.Right {
			return flowReference{}, failure(400, "flow_invalid_crop")
		}
		reference.CropCoordinates = &value
	}
	return reference, nil
}

func flowIdentifier(value string) bool {
	return value != "" && len(value) <= 256 && !strings.ContainsAny(value, " \t\r\n")
}

func flowIdentifiers(values []string) ([]string, error) {
	for _, value := range values {
		if !flowIdentifier(value) {
			return nil, failure(400, "flow_invalid_identifier")
		}
	}
	return slices.Clone(values), nil
}
