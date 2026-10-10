package main

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	flowInputImage = "flow-nano-banana-2"
	flowInputFast  = "flow-veo-3.1-fast"
	flowInputOmni  = "flow-omni-1.1-flash"
)

func flowInputParse(t *testing.T, id, body string) (flowInput, error) {
	t.Helper()
	model, ok := flowModelFor(id)
	if !ok {
		t.Fatalf("unknown model %s", id)
	}
	return parseFlowRequest(model, []byte(body))
}

func flowInputConfig(config string) string {
	return `{"contents":[{"parts":[{"text":"a fox"}]}],"generationConfig":` + config + `}`
}

func flowInputBare(config string) string {
	return `{"generationConfig":` + config + `}`
}

func flowInputMust(t *testing.T, id, body string) flowInput {
	t.Helper()
	input, err := flowInputParse(t, id, body)
	if err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	return input
}

func TestFlowInputDefaults(t *testing.T) {
	video := flowInputMust(t, flowInputFast, flowInputConfig(`{}`))
	if video.count != 1 || video.seed != nil || video.resolution != "720p" || video.aspect != "9:16" || video.seconds != 8 ||
		video.options.Mode != flowModeText || video.options.Priority != "normal" || video.prompt != "a fox" {
		t.Fatalf("video = %+v", video)
	}
	image := flowInputMust(t, flowInputImage, flowInputConfig(`{}`))
	if image.count != 1 || image.seed != nil || image.resolution != "" || image.aspect != "1:1" || image.options.Mode != flowModeText {
		t.Fatalf("image = %+v", image)
	}
}

func TestFlowInputValuesSurvive(t *testing.T) {
	image := flowInputMust(t, flowInputImage, flowInputConfig(`{"candidateCount":3,"seed":42,"imageConfig":{"aspectRatio":"3:4","imageSize":"2k"}}`))
	if image.count != 3 || image.seed == nil || *image.seed != 42 || image.aspect != "3:4" || image.imageSize != "2K" {
		t.Fatalf("image = %+v", image)
	}
	for _, seed := range []int{0, flowMaxSeed} {
		input := flowInputMust(t, flowInputImage, flowInputConfig(`{"seed":`+strconv.Itoa(seed)+`}`))
		if input.seed == nil || *input.seed != seed {
			t.Fatalf("seed %d parsed as %v", seed, input.seed)
		}
	}
	video := flowInputMust(t, flowInputOmni, flowInputConfig(`{"candidateCount":4,"resolution":"4k","durationSeconds":10,"aspectRatio":"16:9"}`))
	if video.count != 4 || video.resolution != "4K" || video.seconds != 10 || video.aspect != "16:9" {
		t.Fatalf("video = %+v", video)
	}
	for given, want := range map[string]string{"360p": "360p", "720P": "720p", "1080p": "1080p", "4K": "4K"} {
		if got := flowInputMust(t, flowInputOmni, flowInputConfig(`{"resolution":"`+given+`"}`)).resolution; got != want {
			t.Errorf("resolution %s = %s, want %s", given, got, want)
		}
	}
	same := flowInputMust(t, flowInputImage, flowInputConfig(`{"aspectRatio":"4:3","imageConfig":{"aspectRatio":"4:3"}}`))
	if same.aspect != "4:3" {
		t.Fatalf("aspect = %s", same.aspect)
	}
}

func TestFlowInputReadsTheFlowObject(t *testing.T) {
	input := flowInputMust(t, flowInputOmni, flowInputConfig(`{"flow":{"mode":"references","priority":"low","projectId":"proj-1","modelKey":"abra_r2v_8s",
		"referenceImages":[{"mediaId":"m1","cropCoordinates":{"top":0,"left":0.25,"bottom":1,"right":0.75}},{"inlineData":{"mimeType":"image/png","data":"AAAA"}}],
		"referenceAudio":["a1"],"referenceLikenesses":["l1"],"referenceEntities":["e1","e2"],"audioFailurePreference":"require_audio",
		"destination":{"workflowId":"w1","collectionId":"c1","sceneId":"s1","position":0}}}`))
	options := input.options
	if options.Mode != flowModeReferences || options.Priority != "low" || options.ProjectID != "proj-1" || options.ModelKey != "abra_r2v_8s" ||
		options.AudioFailurePreference != "require_audio" || !slices.Equal(options.ReferenceAudio, []string{"a1"}) ||
		!slices.Equal(options.ReferenceLikenesses, []string{"l1"}) || !slices.Equal(options.ReferenceEntities, []string{"e1", "e2"}) {
		t.Fatalf("options = %+v", options)
	}
	first, second := options.ReferenceImages[0], options.ReferenceImages[1]
	if first.MediaID != "m1" || first.CropCoordinates == nil || *first.CropCoordinates != (flowCrop{Top: 0, Left: 0.25, Bottom: 1, Right: 0.75}) ||
		second.MediaID != "" || second.mimeType != "image/png" || len(second.data) != 3 || second.CropCoordinates != nil {
		t.Fatalf("references = %+v", options.ReferenceImages)
	}
	destination := options.Destination
	if destination == nil || destination.WorkflowID != "w1" || destination.CollectionID != "c1" || destination.SceneID != "s1" || destination.Position == nil || *destination.Position != 0 {
		t.Fatalf("destination = %+v", destination)
	}
	contents := flowInputMust(t, flowInputFast, `{"contents":[{"parts":[{"text":"a fox"},{"inlineData":{"mimeType":"image/png","data":"AAAA"}}]}]}`)
	if len(contents.references) != 1 || contents.options.Mode != flowModeReferences {
		t.Fatalf("contents references = %+v", contents)
	}
}

func TestFlowInputStructuredPrompt(t *testing.T) {
	input := flowInputMust(t, flowInputOmni, flowInputBare(`{"flow":{"referenceImages":[{"mediaId":"m1"}],"referenceLikenesses":["l1"],"referenceEntities":["e1"],"referenceAudio":["a1"],
		"structuredPrompt":{"parts":[{"text":"a fox with "},{"reference":{"mediaId":"m1"}},{"reference":{"likenessId":"l1"}},{"reference":{"entityId":"e1"}},{"reference":{"audioId":"a1"}}]}}}`))
	want := []flowPromptPart{{Text: "a fox with "}, {MediaID: "m1"}, {LikenessID: "l1"}, {EntityID: "e1"}, {AudioID: "a1"}}
	if !slices.Equal(input.options.StructuredPrompt, want) || input.prompt != "" {
		t.Fatalf("structured prompt = %+v", input.options.StructuredPrompt)
	}
}

func TestFlowInputModes(t *testing.T) {
	frames := flowInputMust(t, flowInputFast, flowInputConfig(`{"flow":{"firstFrame":{"mediaId":"f1"},"lastFrame":{"inlineData":{"mimeType":"image/jpeg","data":"AAAA"}}}}`))
	if frames.options.Mode != flowModeFrames || frames.options.FirstFrame.MediaID != "f1" || len(frames.options.LastFrame.data) != 3 {
		t.Fatalf("frames = %+v", frames.options)
	}
	edit := flowInputMust(t, flowInputOmni, flowInputConfig(`{"flow":{"mode":"edit","sourceVideo":{"mediaId":"v1","startFrame":0,"endFrame":1},"referenceImages":[{"mediaId":"m1"}]}}`))
	if edit.options.Mode != flowModeEdit || edit.options.SourceVideo.MediaID != "v1" || *edit.options.SourceVideo.StartFrame != 0 || *edit.options.SourceVideo.EndFrame != 1 {
		t.Fatalf("edit = %+v", edit.options)
	}
	extend := flowInputMust(t, flowInputOmni, flowInputConfig(`{"flow":{"mode":"extend","sourceVideo":{"mediaId":"v1"}}}`))
	if extend.options.Mode != flowModeExtend || extend.options.SourceVideo.StartFrame != nil {
		t.Fatalf("extend = %+v", extend.options)
	}
	videoUpscale := flowInputMust(t, flowInputOmni, flowInputBare(`{"seed":0,"resolution":"1080p","flow":{"mode":"upscale","sourceVideo":{"mediaId":"v1"}}}`))
	if videoUpscale.options.Mode != flowModeUpscale || videoUpscale.prompt != "" || videoUpscale.resolution != "1080p" || videoUpscale.seed == nil || *videoUpscale.seed != 0 {
		t.Fatalf("video upscale = %+v", videoUpscale)
	}
	imageEdit := flowInputMust(t, flowInputImage, flowInputConfig(`{"flow":{"baseImage":{"mediaId":"b1"},"referenceImages":[{"mediaId":"m1"}]}}`))
	if imageEdit.options.Mode != flowModeEdit || imageEdit.options.BaseImage.MediaID != "b1" {
		t.Fatalf("image edit = %+v", imageEdit.options)
	}
	imageUpscale := flowInputMust(t, flowInputImage, flowInputBare(`{"imageConfig":{"imageSize":"4K"},"flow":{"mode":"upscale","baseImage":{"mediaId":"b1"}}}`))
	if imageUpscale.options.Mode != flowModeUpscale || imageUpscale.prompt != "" || imageUpscale.imageSize != "4K" || imageUpscale.seed != nil {
		t.Fatalf("image upscale = %+v", imageUpscale)
	}
	if mode := flowInputMust(t, flowInputFast, flowInputConfig(`{"flow":{"mode":"auto"}}`)).options.Mode; mode != flowModeText {
		t.Fatalf("auto = %s", mode)
	}
}

func TestFlowInputReferenceCounts(t *testing.T) {
	refs := func(count int) string {
		items := make([]string, count)
		for index := range items {
			items[index] = `{"mediaId":"m` + strconv.Itoa(index) + `"}`
		}
		return flowInputConfig(`{"flow":{"referenceImages":[` + strings.Join(items, ",") + `]}}`)
	}
	flowInputMust(t, flowInputImage, refs(10))
	flowInputMust(t, flowInputOmni, refs(11))
	if _, err := flowInputParse(t, flowInputImage, refs(11)); safeCredentialCode(err) != "flow_too_many_references" {
		t.Fatalf("eleven image references = %v", err)
	}
	if _, err := flowInputParse(t, flowInputImage, flowInputConfig(`{"flow":{"baseImage":{"mediaId":"b"},"referenceImages":[`+strings.Repeat(`{"mediaId":"m"},`, 9)+`{"mediaId":"m"}]}}`)); safeCredentialCode(err) != "flow_too_many_references" {
		t.Fatalf("base plus ten references = %v", err)
	}
}

func TestFlowInputRefusesInvalidValues(t *testing.T) {
	const crop = `"cropCoordinates":`
	for name, test := range map[string]struct {
		model, body, code string
	}{
		"zero candidates":          {flowInputImage, flowInputConfig(`{"candidateCount":0}`), "flow_invalid_candidate_count"},
		"five candidates":          {flowInputImage, flowInputConfig(`{"candidateCount":5}`), "flow_invalid_candidate_count"},
		"string candidates":        {flowInputImage, flowInputConfig(`{"candidateCount":"2"}`), "flow_invalid_candidate_count"},
		"fractional candidates":    {flowInputImage, flowInputConfig(`{"candidateCount":1.5}`), "flow_invalid_candidate_count"},
		"negative seed":            {flowInputImage, flowInputConfig(`{"seed":-1}`), "flow_invalid_seed"},
		"oversized seed":           {flowInputImage, flowInputConfig(`{"seed":2147483648}`), "flow_invalid_seed"},
		"string seed":              {flowInputImage, flowInputConfig(`{"seed":"1"}`), "flow_invalid_seed"},
		"fractional seed":          {flowInputImage, flowInputConfig(`{"seed":1.5}`), "flow_invalid_seed"},
		"video seed":               {flowInputFast, flowInputConfig(`{"seed":1}`), "flow_seed_unsupported"},
		"unknown resolution":       {flowInputOmni, flowInputConfig(`{"resolution":"480p"}`), "flow_invalid_resolution"},
		"image resolution":         {flowInputImage, flowInputConfig(`{"resolution":"720p"}`), "flow_unsupported_generation_option"},
		"upscale without target":   {flowInputOmni, flowInputBare(`{"flow":{"mode":"upscale","sourceVideo":{"mediaId":"v"}}}`), "flow_invalid_resolution"},
		"zero duration":            {flowInputFast, flowInputConfig(`{"durationSeconds":0}`), "flow_invalid_duration"},
		"unknown config key":       {flowInputImage, flowInputConfig(`{"negativePrompt":"x"}`), "flow_unsupported_generation_option"},
		"unknown flow key":         {flowInputFast, flowInputConfig(`{"flow":{"bogus":1}}`), "flow_unsupported_generation_option"},
		"unknown image key":        {flowInputImage, flowInputConfig(`{"imageConfig":{"bogus":1}}`), "flow_unsupported_generation_option"},
		"unknown crop key":         {flowInputFast, flowInputConfig(`{"flow":{"referenceImages":[{"mediaId":"m",` + crop + `{"top":0,"left":0,"bottom":1,"right":1,"bogus":1}}]}}`), "flow_unsupported_generation_option"},
		"unknown reference key":    {flowInputFast, flowInputConfig(`{"flow":{"firstFrame":{"mediaId":"m","bogus":1}}}`), "flow_unsupported_generation_option"},
		"flow not an object":       {flowInputFast, flowInputConfig(`{"flow":"x"}`), "flow_unsupported_generation_option"},
		"flow string mode":         {flowInputFast, flowInputConfig(`{"flow":{"mode":1}}`), "flow_unsupported_generation_option"},
		"conflicting aspect":       {flowInputImage, flowInputConfig(`{"aspectRatio":"1:1","imageConfig":{"aspectRatio":"16:9"}}`), "flow_conflicting_aspect_ratio"},
		"unnamed image ratio":      {flowInputImage, flowInputConfig(`{"aspectRatio":"21:9"}`), "flow_invalid_aspect_ratio"},
		"unknown mode":             {flowInputFast, flowInputConfig(`{"flow":{"mode":"remix"}}`), "flow_invalid_mode"},
		"unknown priority":         {flowInputFast, flowInputConfig(`{"flow":{"priority":"high"}}`), "flow_invalid_priority"},
		"unknown audio preference": {flowInputFast, flowInputConfig(`{"flow":{"audioFailurePreference":"mute"}}`), "flow_invalid_audio_preference"},
		"project with space":       {flowInputFast, flowInputConfig(`{"flow":{"projectId":"a b"}}`), "flow_invalid_identifier"},
		"empty audio id":           {flowInputFast, flowInputConfig(`{"flow":{"referenceAudio":[""]}}`), "flow_invalid_identifier"},
		"empty entity id":          {flowInputFast, flowInputConfig(`{"flow":{"referenceEntities":[""]}}`), "flow_invalid_identifier"},
		"crop top equals bottom":   {flowInputFast, flowInputConfig(`{"flow":{"referenceImages":[{"mediaId":"m",` + crop + `{"top":0.5,"left":0,"bottom":0.5,"right":1}}]}}`), "flow_invalid_crop"},
		"crop left past right":     {flowInputFast, flowInputConfig(`{"flow":{"referenceImages":[{"mediaId":"m",` + crop + `{"top":0,"left":0.8,"bottom":1,"right":0.2}}]}}`), "flow_invalid_crop"},
		"crop below zero":          {flowInputFast, flowInputConfig(`{"flow":{"referenceImages":[{"mediaId":"m",` + crop + `{"top":-0.1,"left":0,"bottom":1,"right":1}}]}}`), "flow_invalid_crop"},
		"crop above one":           {flowInputFast, flowInputConfig(`{"flow":{"referenceImages":[{"mediaId":"m",` + crop + `{"top":0,"left":0,"bottom":1.1,"right":1}}]}}`), "flow_invalid_crop"},
		"crop missing edge":        {flowInputFast, flowInputConfig(`{"flow":{"referenceImages":[{"mediaId":"m",` + crop + `{"top":0,"left":0,"bottom":1}}]}}`), "flow_invalid_crop"},
		"reference with both":      {flowInputFast, flowInputConfig(`{"flow":{"referenceImages":[{"mediaId":"m","inlineData":{"mimeType":"image/png","data":"AAAA"}}]}}`), "flow_invalid_reference"},
		"reference with neither":   {flowInputFast, flowInputConfig(`{"flow":{"referenceImages":[{}]}}`), "flow_invalid_reference"},
		"media id with space":      {flowInputFast, flowInputConfig(`{"flow":{"referenceImages":[{"mediaId":"m 1"}]}}`), "flow_invalid_reference"},
		"pdf reference":            {flowInputFast, flowInputConfig(`{"flow":{"referenceImages":[{"inlineData":{"mimeType":"application/pdf","data":"AAAA"}}]}}`), "flow_reference_type_unsupported"},
		"undecodable reference":    {flowInputFast, flowInputConfig(`{"flow":{"referenceImages":[{"inlineData":{"mimeType":"image/png","data":"!!"}}]}}`), "flow_reference_invalid"},
		"source without id":        {flowInputOmni, flowInputConfig(`{"flow":{"mode":"extend","sourceVideo":{}}}`), "flow_invalid_source_video"},
		"negative start frame":     {flowInputOmni, flowInputConfig(`{"flow":{"mode":"extend","sourceVideo":{"mediaId":"v","startFrame":-1}}}`), "flow_invalid_source_video"},
		"negative end frame":       {flowInputOmni, flowInputConfig(`{"flow":{"mode":"extend","sourceVideo":{"mediaId":"v","endFrame":-1}}}`), "flow_invalid_source_video"},
		"equal frames":             {flowInputOmni, flowInputConfig(`{"flow":{"mode":"extend","sourceVideo":{"mediaId":"v","startFrame":5,"endFrame":5}}}`), "flow_invalid_source_video"},
		"reversed frames":          {flowInputOmni, flowInputConfig(`{"flow":{"mode":"extend","sourceVideo":{"mediaId":"v","startFrame":6,"endFrame":5}}}`), "flow_invalid_source_video"},
		"fractional frame":         {flowInputOmni, flowInputConfig(`{"flow":{"mode":"extend","sourceVideo":{"mediaId":"v","startFrame":1.5}}}`), "flow_unsupported_generation_option"},
		"empty structured parts":   {flowInputFast, flowInputBare(`{"flow":{"structuredPrompt":{"parts":[]}}}`), "flow_invalid_structured_prompt"},
		"empty structured part":    {flowInputFast, flowInputBare(`{"flow":{"structuredPrompt":{"parts":[{}]}}}`), "flow_invalid_structured_prompt"},
		"text and reference part":  {flowInputFast, flowInputBare(`{"flow":{"structuredPrompt":{"parts":[{"text":"a","reference":{"mediaId":"m"}}]}}}`), "flow_invalid_structured_prompt"},
		"two reference ids":        {flowInputFast, flowInputBare(`{"flow":{"structuredPrompt":{"parts":[{"reference":{"mediaId":"m","entityId":"e"}}]}}}`), "flow_invalid_structured_prompt"},
		"empty reference":          {flowInputFast, flowInputBare(`{"flow":{"structuredPrompt":{"parts":[{"reference":{}}]}}}`), "flow_invalid_structured_prompt"},
		"empty destination":        {flowInputFast, flowInputConfig(`{"flow":{"destination":{}}}`), "flow_invalid_destination"},
		"negative position":        {flowInputFast, flowInputConfig(`{"flow":{"destination":{"sceneId":"s","position":-1}}}`), "flow_invalid_destination"},
		"position without scene":   {flowInputFast, flowInputConfig(`{"flow":{"destination":{"workflowId":"w","position":0}}}`), "flow_invalid_destination"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := flowInputParse(t, test.model, test.body)
			if code := safeCredentialCode(err); code != test.code {
				t.Fatalf("code = %s, want %s (%v)", code, test.code, err)
			}
		})
	}
	_, err := flowInputParse(t, flowInputFast, flowInputConfig(`{"flow":{"bogus":1}}`))
	if err == nil || !strings.Contains(err.Error(), "flow.bogus") {
		t.Fatalf("unknown field is not named: %v", err)
	}
}

func TestFlowInputCropAndFrameBoundaries(t *testing.T) {
	input := flowInputMust(t, flowInputFast, flowInputConfig(`{"flow":{"referenceImages":[{"mediaId":"m","cropCoordinates":{"top":0,"left":0,"bottom":1,"right":1}}]}}`))
	if crop := input.options.ReferenceImages[0].CropCoordinates; crop == nil || *crop != (flowCrop{Top: 0, Left: 0, Bottom: 1, Right: 1}) {
		t.Fatalf("full crop = %+v", crop)
	}
	video := flowInputMust(t, flowInputOmni, flowInputConfig(`{"flow":{"mode":"extend","sourceVideo":{"mediaId":"v","startFrame":0,"endFrame":1}}}`))
	if source := video.options.SourceVideo; *source.StartFrame != 0 || *source.EndFrame != 1 {
		t.Fatalf("source = %+v", source)
	}
}

func TestFlowInputRefusesContradictions(t *testing.T) {
	for name, test := range map[string]struct {
		model, body, code string
	}{
		"text with references":      {flowInputFast, flowInputConfig(`{"flow":{"mode":"text","referenceImages":[{"mediaId":"m"}]}}`), "flow_conflicting_inputs"},
		"text with structured refs": {flowInputFast, flowInputConfig(`{"flow":{"mode":"text","structuredPrompt":{"parts":[{"reference":{"mediaId":"m"}}]}}}`), "flow_conflicting_inputs"},
		"frames with references":    {flowInputFast, flowInputConfig(`{"flow":{"firstFrame":{"mediaId":"f"},"referenceImages":[{"mediaId":"m"}]}}`), "flow_conflicting_inputs"},
		"frames with inline image":  {flowInputFast, `{"contents":[{"parts":[{"text":"x"},{"inlineData":{"mimeType":"image/png","data":"AAAA"}}]}],"generationConfig":{"flow":{"firstFrame":{"mediaId":"f"}}}}`, "flow_conflicting_inputs"},
		"references with frames":    {flowInputFast, flowInputConfig(`{"flow":{"mode":"references","firstFrame":{"mediaId":"f"},"referenceImages":[{"mediaId":"m"}]}}`), "flow_conflicting_inputs"},
		"frames with source":        {flowInputOmni, flowInputConfig(`{"flow":{"mode":"frames","firstFrame":{"mediaId":"f"},"sourceVideo":{"mediaId":"v"}}}`), "flow_conflicting_inputs"},
		"extend with frames":        {flowInputOmni, flowInputConfig(`{"flow":{"mode":"extend","firstFrame":{"mediaId":"f"},"sourceVideo":{"mediaId":"v"}}}`), "flow_conflicting_inputs"},
		"upscale with references":   {flowInputOmni, flowInputBare(`{"resolution":"4k","flow":{"mode":"upscale","sourceVideo":{"mediaId":"v"},"referenceImages":[{"mediaId":"m"}]}}`), "flow_conflicting_inputs"},
		"upscale with structured":   {flowInputOmni, flowInputBare(`{"resolution":"4k","flow":{"mode":"upscale","sourceVideo":{"mediaId":"v"},"structuredPrompt":{"parts":[{"text":"x"}]}}}`), "flow_conflicting_inputs"},
		"image edit with refs only": {flowInputImage, flowInputConfig(`{"flow":{"mode":"edit","referenceImages":[{"mediaId":"m"}]}}`), "flow_input_required"},
		"references without any":    {flowInputFast, flowInputConfig(`{"flow":{"mode":"references"}}`), "flow_input_required"},
		"last frame alone":          {flowInputFast, flowInputConfig(`{"flow":{"lastFrame":{"mediaId":"f"}}}`), "flow_input_required"},
		"extend without source":     {flowInputOmni, flowInputConfig(`{"flow":{"mode":"extend"}}`), "flow_input_required"},
		"video edit without source": {flowInputOmni, flowInputConfig(`{"flow":{"mode":"edit"}}`), "flow_input_required"},
		"image upscale without":     {flowInputImage, flowInputBare(`{"imageConfig":{"imageSize":"2K"},"flow":{"mode":"upscale"}}`), "flow_input_required"},
		"source needs a mode":       {flowInputOmni, flowInputConfig(`{"flow":{"sourceVideo":{"mediaId":"v"}}}`), "flow_mode_required"},
		"image frames mode":         {flowInputImage, flowInputConfig(`{"flow":{"mode":"frames"}}`), "flow_mode_unsupported"},
		"image extend mode":         {flowInputImage, flowInputConfig(`{"flow":{"mode":"extend"}}`), "flow_mode_unsupported"},
		"image first frame":         {flowInputImage, flowInputConfig(`{"flow":{"firstFrame":{"mediaId":"f"}}}`), "flow_unsupported_generation_option"},
		"image source video":        {flowInputImage, flowInputConfig(`{"flow":{"sourceVideo":{"mediaId":"v"}}}`), "flow_unsupported_generation_option"},
		"image audio":               {flowInputImage, flowInputConfig(`{"flow":{"referenceAudio":["a"]}}`), "flow_unsupported_generation_option"},
		"image audio preference":    {flowInputImage, flowInputConfig(`{"flow":{"audioFailurePreference":"allow_silent"}}`), "flow_unsupported_generation_option"},
		"video base image":          {flowInputFast, flowInputConfig(`{"flow":{"baseImage":{"mediaId":"b"}}}`), "flow_unsupported_generation_option"},
		"image upscale inline base": {flowInputImage, flowInputBare(`{"imageConfig":{"imageSize":"2K"},"flow":{"mode":"upscale","baseImage":{"inlineData":{"mimeType":"image/png","data":"AAAA"}}}}`), "flow_invalid_reference"},
		"image upscale cropped":     {flowInputImage, flowInputBare(`{"imageConfig":{"imageSize":"2K"},"flow":{"mode":"upscale","baseImage":{"mediaId":"b","cropCoordinates":{"top":0,"left":0,"bottom":1,"right":1}}}}`), "flow_invalid_reference"},
		"image upscale size":        {flowInputImage, flowInputBare(`{"imageConfig":{"imageSize":"1K"},"flow":{"mode":"upscale","baseImage":{"mediaId":"b"}}}`), "flow_invalid_image_size"},
		"image upscale no size":     {flowInputImage, flowInputBare(`{"flow":{"mode":"upscale","baseImage":{"mediaId":"b"}}}`), "flow_invalid_image_size"},
		"upscale many candidates":   {flowInputImage, flowInputBare(`{"candidateCount":2,"imageConfig":{"imageSize":"2K"},"flow":{"mode":"upscale","baseImage":{"mediaId":"b"}}}`), "flow_invalid_candidate_count"},
		"video seed in video edit":  {flowInputOmni, flowInputConfig(`{"seed":3,"flow":{"mode":"edit","sourceVideo":{"mediaId":"v"}}}`), "flow_seed_unsupported"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := flowInputParse(t, test.model, test.body)
			if code := safeCredentialCode(err); code != test.code {
				t.Fatalf("code = %s, want %s (%v)", code, test.code, err)
			}
		})
	}
}

func TestFlowInputPromptRules(t *testing.T) {
	for name, test := range map[string]struct {
		model, body, code string
	}{
		"text mode without prompt":    {flowInputFast, flowInputBare(`{}`), "flow_prompt_required"},
		"blank prompt":                {flowInputImage, `{"contents":[{"parts":[{"text":"  "}]}]}`, "flow_prompt_required"},
		"extend without prompt":       {flowInputOmni, flowInputBare(`{"flow":{"mode":"extend","sourceVideo":{"mediaId":"v"}}}`), "flow_prompt_required"},
		"image edit without prompt":   {flowInputImage, flowInputBare(`{"flow":{"baseImage":{"mediaId":"b"}}}`), "flow_prompt_required"},
		"structured without text":     {flowInputFast, flowInputBare(`{"flow":{"referenceImages":[{"mediaId":"m"}],"structuredPrompt":{"parts":[{"reference":{"mediaId":"m"}}]}}}`), "flow_prompt_required"},
		"structured plus text prompt": {flowInputFast, flowInputConfig(`{"flow":{"structuredPrompt":{"parts":[{"text":"other"}]}}}`), "flow_prompt_conflict"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := flowInputParse(t, test.model, test.body)
			if code := safeCredentialCode(err); code != test.code {
				t.Fatalf("code = %s, want %s (%v)", code, test.code, err)
			}
		})
	}
	flowInputMust(t, flowInputFast, flowInputBare(`{"flow":{"structuredPrompt":{"parts":[{"text":"a fox"}]}}}`))
}

func TestFlowImageAspectMatchesTheLiveBundle(t *testing.T) {
	for aspect, want := range map[string]int{"1:1": 1, "9:16": 2, "16:9": 3, "3:4": 4, "4:3": 5, "21:9": 0, "5:4": 0, "": 0} {
		if got := flowImageAspect(aspect); got != want {
			t.Errorf("flowImageAspect(%q) = %d, want %d", aspect, got, want)
		}
	}
}
