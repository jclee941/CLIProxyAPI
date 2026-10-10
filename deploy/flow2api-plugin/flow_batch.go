package main

// flowPromptWire preserves explicit ingredient references rather than flattening
// them into text. The union positions are media, audio, entity and likeness.
func flowPromptWire(input flowInput) []any {
	if len(input.options.StructuredPrompt) == 0 {
		return []any{[]any{[]any{input.prompt}}}
	}
	parts := make([]any, 0, len(input.options.StructuredPrompt))
	for _, part := range input.options.StructuredPrompt {
		if !part.isReference() {
			parts = append(parts, []any{part.Text})
			continue
		}
		reference := make([]any, 4)
		for index, id := range []string{part.MediaID, part.AudioID, part.EntityID, part.LikenessID} {
			if id != "" {
				reference[index] = []any{id}
			}
		}
		parts = append(parts, []any{nil, reference})
	}
	return []any{parts}
}

func flowReferenceWireValue(reference *flowReference) any {
	if reference == nil {
		return nil
	}
	var crop any
	if value := reference.CropCoordinates; value != nil {
		crop = []any{value.Top, value.Left, value.Bottom, value.Right}
	}
	return []any{nil, reference.MediaID, nil, nil, nil, crop}
}

func flowReferenceIDs(ids []string) []any {
	values := make([]any, len(ids))
	for index, id := range ids {
		values[index] = []any{id}
	}
	return values
}

func flowBatchArgs(project, token string, input flowInput, selected flowSelected) (string, []any) {
	context := flowContext(project, token)
	options := input.options
	group := []any{flowID()}
	audioPreference := 2
	if options.AudioFailurePreference == "allow_silent" {
		audioPreference = 1
	}
	if selected.usage.video {
		group = append(group, audioPreference)
	}
	var scene, workflow, collection any
	if destination := options.Destination; destination != nil {
		if destination.SceneID != "" {
			scene = destination.SceneID
			if destination.Position != nil {
				for len(group) < 4 {
					group = append(group, nil)
				}
				group[3] = []any{destination.SceneID, *destination.Position}
			}
		}
		if destination.WorkflowID != "" {
			workflow = destination.WorkflowID
		}
		if destination.CollectionID != "" {
			collection = destination.CollectionID
		}
	}
	if !selected.usage.video {
		context[4], context[7] = workflow, collection
	}
	prompt := flowPromptWire(input)
	videoPrompt := []any{nil, nil, prompt}
	images := make([]any, len(input.references))
	for index := range input.references {
		if selected.usage.video {
			images[index] = flowReferenceWireValue(&input.references[index])
		} else {
			images[index] = []any{input.references[index].MediaID, nil, nil, nil, 1}
		}
	}
	if options.BaseImage != nil && !selected.usage.video {
		images = append([]any{[]any{options.BaseImage.MediaID, nil, nil, nil, 2}}, images...)
	}
	var source any
	if video := options.SourceVideo; video != nil {
		source = []any{nil, video.MediaID, video.StartFrame, video.EndFrame}
	}
	var output any
	if selected.resolution != 1 {
		output = []any{selected.resolution}
	}
	count := input.count
	requests := make([]any, count)
	rpc := "ogiZ0b"
	for index := range count {
		var newWorkflow any = flowID()
		if workflow != nil {
			newWorkflow = nil
		}
		mediaID := flowID()
		metadata := []any{scene, workflow, collection, nil, mediaID, newWorkflow}
		key, aspect := selected.usage.key, selected.aspect
		var request []any
		if !selected.usage.video {
			seed := flowSeed()
			if input.seed != nil {
				seed = *input.seed
			}
			request = []any{nil, nil, images, seed, aspect, key, nil, context, prompt, nil,
				flowReferenceIDs(options.ReferenceEntities), flowReferenceIDs(options.ReferenceLikenesses), newWorkflow, mediaID}
		} else {
			switch selected.mode {
			case "text":
				rpc = "YhhmEf"
				request = []any{videoPrompt, key, aspect, nil, metadata, nil, nil, output}
			case "references":
				rpc = "MZZa6b"
				request = []any{videoPrompt, images, key, aspect, nil, metadata, nil,
					flowReferenceIDs(options.ReferenceAudio), nil, flowReferenceIDs(options.ReferenceEntities),
					flowReferenceIDs(options.ReferenceLikenesses), output}
			case "frames":
				if options.LastFrame == nil {
					rpc = "eb1hJf"
					request = []any{videoPrompt, key, aspect, nil, flowReferenceWireValue(options.FirstFrame), metadata, nil, nil, nil, output}
				} else {
					rpc = "nprQif"
					request = []any{videoPrompt, key, aspect, nil, flowReferenceWireValue(options.FirstFrame),
						flowReferenceWireValue(options.LastFrame), metadata, nil, nil}
				}
			case "edit":
				rpc = "jIps6"
				request = []any{source, videoPrompt, key, aspect, metadata, nil, nil, nil, images,
					flowReferenceIDs(options.ReferenceAudio), flowReferenceIDs(options.ReferenceEntities),
					flowReferenceIDs(options.ReferenceLikenesses), output}
			case "extend":
				rpc = "fZytfe"
				request = []any{source, videoPrompt, key, aspect, nil, metadata, nil, nil, images,
					flowReferenceIDs(options.ReferenceAudio), flowReferenceIDs(options.ReferenceEntities),
					flowReferenceIDs(options.ReferenceLikenesses), output}
			case "upscale":
				rpc = "p0UkFb"
				request = make([]any, 32)
				request[0], request[2], request[3], request[4], request[6], request[31] =
					source, aspect, input.seed, metadata, selected.resolution, key
			}
		}
		requests[index] = request
	}
	if selected.usage.video {
		return rpc, []any{requests, context, group}
	}
	return rpc, []any{nil, requests, true, context, group}
}
