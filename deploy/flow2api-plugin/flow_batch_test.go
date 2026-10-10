package main

import (
	"encoding/json"
	"testing"
)

func TestFlowImageBatchHonorsSeedCountAndDestination(t *testing.T) {
	// Given: an explicit zero seed and two outputs in an existing workflow.
	zero := 0
	input := flowInput{prompt: "fixture", count: 2, seed: &zero,
		references: []flowReference{{MediaID: "reference"}},
		options:    flowOptions{Destination: &flowDestination{WorkflowID: "workflow", CollectionID: "collection"}}}
	selected := flowSelected{usage: flowModelUsage{key: "BELUGA"}, aspect: 4}

	// When: a single image batch is serialized.
	rpc, args := flowBatchArgs("project", "captcha", input, selected)
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	var wire any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}

	// Then: both samples retain the caller seed and target, with unique IDs.
	if rpc != "ogiZ0b" || len(jsonField(wire, 1).([]any)) != 2 {
		t.Fatalf("batch = %s %s", rpc, raw)
	}
	for index := range 2 {
		if seed, _ := jsonInteger(jsonField(wire, 1, index, 3)); seed != 0 {
			t.Fatalf("seed = %d", seed)
		}
		if aspect, _ := jsonInteger(jsonField(wire, 1, index, 4)); aspect != 4 {
			t.Fatalf("aspect = %d", aspect)
		}
		if jsonField(wire, 1, index, 7, 4) != "workflow" ||
			jsonField(wire, 1, index, 7, 7) != "collection" ||
			jsonField(wire, 1, index, 2, 0, 0) != "reference" {
			t.Fatalf("sample lost caller parameters: %s", raw)
		}
	}
	if jsonField(wire, 1, 0, 13) == jsonField(wire, 1, 1, 13) {
		t.Fatal("batch reused output media ID")
	}
}

func TestFlowVideoBatchUsesOperationSpecificSlots(t *testing.T) {
	for _, scenario := range []struct {
		mode, rpc string
		metadata  int
	}{
		{"text", "YhhmEf", 4}, {"references", "MZZa6b", 5},
		{"frames", "nprQif", 6}, {"edit", "jIps6", 4},
		{"extend", "fZytfe", 5}, {"upscale", "p0UkFb", 4},
	} {
		t.Run(scenario.mode, func(t *testing.T) {
			// Given: distinct values for every meaningful input and two samples.
			start, end, seed := 3, 45, 19
			input := flowInput{prompt: "fixture", count: 2, seed: &seed,
				references: []flowReference{{MediaID: "ingredient"}},
				options: flowOptions{
					FirstFrame:     &flowReference{MediaID: "start", CropCoordinates: &flowCrop{Top: .1, Left: .2, Bottom: .8, Right: .9}},
					LastFrame:      &flowReference{MediaID: "end"},
					SourceVideo:    &flowVideoReference{MediaID: "video", StartFrame: &start, EndFrame: &end},
					ReferenceAudio: []string{"audio"}, ReferenceEntities: []string{"entity"},
					ReferenceLikenesses: []string{"likeness"}, AudioFailurePreference: "allow_silent",
				}}
			selected := flowSelected{mode: scenario.mode, usage: flowModelUsage{key: "selected-key", video: true}, aspect: 1, resolution: 4}

			// When: the matching current-web RPC is built.
			rpc, args := flowBatchArgs("project", "captcha", input, selected)

			// Then: operation-specific slots preserve the exact inputs.
			if rpc != scenario.rpc || len(args[0].([]any)) != 2 || jsonField(args, 2, 1) != 1 {
				t.Fatalf("RPC %s args %#v", rpc, args)
			}
			a, b := jsonField(args, 0, 0, scenario.metadata, 4), jsonField(args, 0, 1, scenario.metadata, 4)
			if a == nil || a == b {
				t.Fatal("output identities missing or reused")
			}
			switch scenario.mode {
			case "text":
				if jsonField(args, 0, 0, 7, 0) != 4 {
					t.Fatal("resolution was not nested in output spec")
				}
			case "references":
				if jsonField(args, 0, 0, 7, 0, 0) != "audio" || jsonField(args, 0, 0, 10, 0, 0) != "likeness" {
					t.Fatal("reference types were lost")
				}
			case "frames":
				if jsonField(args, 0, 0, 4, 5, 0) != .1 || jsonField(args, 0, 0, 5, 1) != "end" {
					t.Fatal("first/end frame or crop lost")
				}
			case "edit", "extend":
				if jsonField(args, 0, 0, 0, 1) != "video" || jsonField(args, 0, 0, 0, 2) != &start {
					t.Fatal("source clip bounds lost")
				}
			case "upscale":
				if jsonField(args, 0, 0, 31) != "selected-key" || jsonField(args, 0, 0, 3) != &seed {
					t.Fatal("upscale key or seed lost")
				}
			}
		})
	}
}
