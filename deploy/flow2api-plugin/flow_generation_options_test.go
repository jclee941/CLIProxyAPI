package main

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestFlowMultipleImagesAreOneSubmissionWithRecoverableReferences(t *testing.T) {
	// Given: Flow returns two outputs for one batch.
	fixture := newFlowFixture(t)
	second := "22222222-3333-4444-8555-666666666666"
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("ogiZ0b", rpcEnvelope(t, "ogiZ0b", []any{[]any{
		[]any{flowTestMedia, fixture.link("image")},
		[]any{second, fixture.link("image")},
	}}))
	service, record := flowService(t, fixture)

	// When: the caller chooses two candidates and an explicit zero seed.
	result := flowExecute(t, service, record, "flow-nano-banana-2",
		`{"contents":[{"parts":[{"text":"fixture"}]}],"generationConfig":{"candidateCount":2,"seed":0,"imageConfig":{"aspectRatio":"3:4"}}}`)

	// Then: one generation preserves both artifacts and their Flow identity.
	if !result.OK {
		t.Fatalf("generation error = %+v", result.Error)
	}
	var response struct{ Payload []byte }
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Flow flowOutMediaReference `json:"flow"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(response.Payload, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Candidates) != 2 || body.Candidates[0].Content.Parts[0].Flow.MediaID != flowTestMedia ||
		body.Candidates[1].Content.Parts[0].Flow.MediaID != second ||
		body.Candidates[1].Content.Parts[0].Flow.ProjectID != flowTestProject {
		t.Fatalf("media identity missing: %s", response.Payload)
	}
	args := fixture.args("ogiZ0b", 0)
	if fixture.count("ogiZ0b") != 1 || len(jsonField(args, 1).([]any)) != 2 ||
		jsonField(args, 1, 0, 3) != float64(0) || jsonField(args, 1, 1, 4) != float64(4) {
		t.Fatalf("batch options = %+v", args)
	}
}

func TestFlowFramesUseCurrentModelAndCallerProject(t *testing.T) {
	// Given: an existing project and reusable frame media IDs.
	fixture := newFlowFixture(t)
	record := []any{flowTestOp, flowTestProject, flowTestMedia}
	fixture.reply("nprQif", rpcEnvelope(t, "nprQif", []any{[]any{record}}))
	fixture.reply("jwpduf", rpcEnvelope(t, "jwpduf", []any{[]any{append(slices.Clone(record), fixture.link("video"))}}))
	service, account := flowService(t, fixture)

	// When: Omni interpolates first/end frames at 360p.
	result := flowExecute(t, service, account, "flow-omni-1.1-flash",
		`{"contents":[{"parts":[{"text":"fixture"}]}],"generationConfig":{"durationSeconds":4,"resolution":"360p","flow":{"mode":"frames","projectId":"`+flowTestProject+`","firstFrame":{"mediaId":"first"},"lastFrame":{"mediaId":"last"}}}}`)

	// Then: the live catalog key and both frame identities reach the RPC.
	flowInlineMedia(t, result)
	args := fixture.args("nprQif", 0)
	if jsonField(args, 0, 0, 1) != "omni_flash_i2v_4s_first_last_360p" ||
		jsonField(args, 0, 0, 4, 1) != "first" || jsonField(args, 0, 0, 5, 1) != "last" ||
		fixture.count("jHPbke") != 0 || fixture.count("maseQ") != 0 {
		t.Fatalf("frame request = %+v", args)
	}
}

func TestFlowUpscaleAdmitsAccountBeforeGenerating(t *testing.T) {
	// Given: the account cannot use 4K upscaling.
	fixture := newFlowFixture(t)
	fixture.mu.Lock()
	fixture.replies["nzlxg"] = []string{rpcEnvelope(t, "nzlxg", []any{100, 2, 2, 2})}
	fixture.mu.Unlock()
	service, record := flowService(t, fixture)

	// When: a caller asks for a new 4K Veo video.
	result := flowExecute(t, service, record, "flow-veo-3.1-lite",
		`{"contents":[{"parts":[{"text":"fixture"}]}],"generationConfig":{"resolution":"4K"}}`)

	// Then: the request is rejected before spending credits on its base video.
	if result.OK || result.Error.Code != "flow_model_options_unsupported" ||
		fixture.count("YhhmEf") != 0 || fixture.count("jHPbke") != 0 {
		t.Fatalf("late admission: %+v", result)
	}
}

func TestFlowExplicitVideoUpscaleUsesSourceAndSeed(t *testing.T) {
	// Given: an existing video can be upscaled without a new text generation.
	fixture := newFlowFixture(t)
	operation := []any{flowTestOp, flowTestProject, flowTestMedia}
	fixture.reply("p0UkFb", rpcEnvelope(t, "p0UkFb", []any{[]any{operation}}))
	fixture.reply("jwpduf", rpcEnvelope(t, "jwpduf", []any{[]any{append(slices.Clone(operation), fixture.link("video"))}}))
	service, record := flowService(t, fixture)

	// When: the caller selects 1080p with a seed.
	result := flowExecute(t, service, record, "flow-veo-3.1-fast",
		`{"generationConfig":{"resolution":"1080p","seed":29,"flow":{"mode":"upscale","projectId":"`+flowTestProject+`","sourceVideo":{"mediaId":"source-video"}}}}`)

	// Then: the current upscale RPC receives the source and its field32 key.
	flowInlineMedia(t, result)
	args := fixture.args("p0UkFb", 0)
	if jsonField(args, 0, 0, 0, 1) != "source-video" || jsonField(args, 0, 0, 3) != float64(29) ||
		jsonField(args, 0, 0, 6) != float64(2) || jsonField(args, 0, 0, 31) != "veo_3_1_upsampler_1080p" ||
		fixture.count("YhhmEf") != 0 {
		t.Fatalf("upscale request = %+v", args)
	}
}

func TestFlowVideoEditAndExtendPreserveTheSourceClip(t *testing.T) {
	for _, scenario := range []struct{ mode, model, rpc, key string }{
		{"edit", "flow-omni-1.1-flash", "jIps6", "abra_edit"},
		{"extend", "flow-veo-3.1-lite", "fZytfe", "veo_3_1_extension_lite"},
	} {
		t.Run(scenario.mode, func(t *testing.T) {
			// Given: an existing video with explicit frame boundaries.
			fixture := newFlowFixture(t)
			operation := []any{flowTestOp, flowTestProject, flowTestMedia}
			fixture.reply(scenario.rpc, rpcEnvelope(t, scenario.rpc, []any{[]any{operation}}))
			fixture.reply("jwpduf", rpcEnvelope(t, "jwpduf", []any{[]any{append(slices.Clone(operation), fixture.link("video"))}}))
			service, record := flowService(t, fixture)

			// When: the selected operation runs without uploading another video.
			result := flowExecute(t, service, record, scenario.model,
				`{"contents":[{"parts":[{"text":"fixture"}]}],"generationConfig":{"flow":{"mode":"`+scenario.mode+`","projectId":"`+flowTestProject+`","sourceVideo":{"mediaId":"source-video","startFrame":3,"endFrame":48}}}}`)

			// Then: its source, clip and model are not replaced by text-to-video.
			flowInlineMedia(t, result)
			args := fixture.args(scenario.rpc, 0)
			if jsonField(args, 0, 0, 0, 1) != "source-video" ||
				jsonField(args, 0, 0, 0, 2) != float64(3) ||
				jsonField(args, 0, 0, 0, 3) != float64(48) ||
				jsonField(args, 0, 0, 2) != scenario.key || fixture.count("YhhmEf") != 0 {
				t.Fatalf("clip request = %+v", args)
			}
		})
	}
}

func TestFlowImageCropAndUpscaleKeepTheirParameters(t *testing.T) {
	// Given: a cropped reference and an image eligible for 2K output.
	fixture := newFlowFixture(t)
	cropped := "22222222-3333-4444-8555-666666666666"
	fixture.reply("sAVZzc", rpcEnvelope(t, "sAVZzc", []any{[]any{cropped}}))
	fixture.reply("ogiZ0b", rpcEnvelope(t, "ogiZ0b", []any{[]any{[]any{flowTestMedia, fixture.link("image")}}}))
	fixture.reply("SPrCad", rpcEnvelope(t, "SPrCad", []any{[]any{flowTestMedia, fixture.link("image")}}))
	service, record := flowService(t, fixture)

	// When: a caller combines the cropped input with 2K output.
	result := flowExecute(t, service, record, "flow-nano-banana-pro",
		`{"contents":[{"parts":[{"text":"fixture"}]}],"generationConfig":{"imageConfig":{"imageSize":"2K"},"flow":{"projectId":"`+flowTestProject+`","referenceImages":[{"mediaId":"source","cropCoordinates":{"top":0.1,"left":0.2,"bottom":0.8,"right":0.9}}]}}}`)

	// Then: the crop result, not the uncropped source, is used and upscaled.
	flowInlineMedia(t, result)
	crop := fixture.args("sAVZzc", 0)
	generate, upscale := fixture.args("ogiZ0b", 0), fixture.args("SPrCad", 0)
	if jsonField(crop, 3, 0) != .1 || jsonField(crop, 3, 3) != .9 ||
		jsonField(generate, 1, 0, 2, 0, 0) != cropped ||
		jsonField(upscale, 0) != flowTestMedia || jsonField(upscale, 1) != float64(1) {
		t.Fatalf("crop=%+v generate=%+v upscale=%+v", crop, generate, upscale)
	}
}
