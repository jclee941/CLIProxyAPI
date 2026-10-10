package main

import "testing"

func TestFlowRefusesOptionsTheSelectedOperationCannotApply(t *testing.T) {
	for _, scenario := range []struct{ name, model, config, code string }{
		{"image upscale seed", "flow-nano-banana-2", `"seed":7,"imageConfig":{"imageSize":"2K"},"flow":{"mode":"upscale","baseImage":{"mediaId":"source"}}`, "flow_seed_unsupported"},
		{"upscale duration", "flow-veo-3.1-fast", `"durationSeconds":4,"resolution":"1080p","flow":{"mode":"upscale","sourceVideo":{"mediaId":"source"}}`, "flow_unsupported_generation_option"},
		{"upscale clip", "flow-veo-3.1-fast", `"resolution":"1080p","flow":{"mode":"upscale","sourceVideo":{"mediaId":"source","startFrame":1}}`, "flow_unsupported_generation_option"},
		{"image priority", "flow-nano-banana-2", `"flow":{"priority":"low"}`, "flow_unsupported_generation_option"},
		{"upscale priority", "flow-veo-3.1-lite", `"resolution":"1080p","flow":{"mode":"upscale","priority":"low","sourceVideo":{"mediaId":"source"}}`, "flow_unsupported_generation_option"},
		{"image scene", "flow-nano-banana-2", `"flow":{"destination":{"sceneId":"scene","position":0}}`, "flow_unsupported_generation_option"},
		{"image upscale destination", "flow-nano-banana-2", `"imageConfig":{"imageSize":"2K"},"flow":{"mode":"upscale","baseImage":{"mediaId":"source"},"destination":{"collectionId":"collection"}}`, "flow_unsupported_generation_option"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given: a syntactically valid option with no field in this RPC.
			model, _ := flowModelFor(scenario.model)
			body := []byte(`{"contents":[{"parts":[{"text":"fixture"}]}],"generationConfig":{` + scenario.config + `}}`)

			// When: the request is parsed before any upstream action.
			_, err := parseFlowRequest(model, body)

			// Then: it fails explicitly instead of pretending to apply it.
			if safeCredentialCode(err) != scenario.code {
				t.Fatalf("error = %v, want %s", err, scenario.code)
			}
		})
	}
}
