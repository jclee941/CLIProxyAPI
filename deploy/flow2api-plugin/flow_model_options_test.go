package main

import (
	"encoding/json"
	"testing"
)

func TestFlowModelCatalogPreservesCurrentOptions(t *testing.T) {
	// Given: real GetModels field layouts for Omni and Nano Banana Pro.
	var payload any
	raw := `[[null,null,null,null,[
["Omni",[
["abra_t2v_4s",null,null,null,[[1,[null,[]]],[3,[[null,7]]]],null,null,[[[[1]]]],null,null,true,null,[[2,1]],null,120,null,4,true,null,null,null,[],null,[[1]]],
["abra_r2v_4s_360p",null,null,null,[[3,[[null,4]]]],null,null,[[[[1,6]],[[1,6,18,19]]]],null,7,true,null,[[2,1]],null,120,null,4,true,null,null,null,[5,3,7],null,[[4]]]
],null,"abra"]],[
["Pro",[["GEM_PIX_2",null,null,null,[[3,[[]]]],null,null,null,[[[[1]],[[1,3]],[[1,3,7,4]]]],10,null,null,null,[[1,2,3,4,5]],40,null,null,true,null,null,null,[null,10,10]]],null,"nano_banana_pro"]]]]`
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}

	// When: the positional catalog is decoded.
	usages, err := decodeFlowModels(payload)
	if err != nil {
		t.Fatal(err)
	}

	// Then: tier admission, resolution, limits and zero-cost entries survive.
	if len(usages) != 3 || usages[0].duration != 4 || usages[0].prices[3] != 7 {
		t.Fatalf("decoded usages = %+v", usages)
	}
	if _, admitted := usages[0].prices[1]; admitted {
		t.Fatal("unavailable tier was admitted")
	}
	if usages[1].resolutions[0] != 4 || usages[1].audioLimit != 5 || usages[1].imageLimit != 7 {
		t.Fatalf("reference limits = %+v", usages[1])
	}
	if credits, admitted := usages[2].prices[3]; !admitted || credits != 0 || usages[2].imageLimit != 10 {
		t.Fatalf("free image model = %+v", usages[2])
	}
}

func TestFlowModelSelectionHonorsEveryRequestedConstraint(t *testing.T) {
	// Given: the text, reference, frame and low-resolution usages differ.
	usages := []flowModelUsage{
		{family: "abra", key: "text", video: true, aspects: []int{1}, resolutions: []int{1}, duration: 4, inputs: [][]int{{1}}, prices: map[int]int{3: 7}},
		{family: "abra", key: "references", video: true, aspects: []int{1}, resolutions: []int{1}, duration: 4, inputs: [][]int{{1, 6}, {1, 6, 18, 19}}, prices: map[int]int{3: 7}, imageLimit: 7, audioLimit: 5},
		{family: "abra", key: "frames", video: true, aspects: []int{1}, resolutions: []int{1}, duration: 4, inputs: [][]int{{1, 5, 7}}, prices: map[int]int{3: 7}},
		{family: "abra", key: "360p", video: true, aspects: []int{1}, resolutions: []int{4}, duration: 4, inputs: [][]int{{1}}, prices: map[int]int{3: 4}},
	}
	for _, scenario := range []struct {
		name   string
		change func(*flowModelSelection)
		key    string
		code   string
	}{
		{name: "text", key: "text"},
		{name: "references", change: func(s *flowModelSelection) { s.inputs = []int{1, 6}; s.images = 5 }, key: "references"},
		{name: "frames", change: func(s *flowModelSelection) { s.inputs = []int{1, 5, 7} }, key: "frames"},
		{name: "360p", change: func(s *flowModelSelection) { s.resolution = 4 }, key: "360p"},
		{name: "wrong tier", change: func(s *flowModelSelection) { s.tier = 1 }, code: "flow_model_options_unsupported"},
		{name: "wrong family", change: func(s *flowModelSelection) { s.family = "veo_3_1_fast" }, code: "flow_model_options_unsupported"},
		{name: "wrong duration", change: func(s *flowModelSelection) { s.duration = 10 }, code: "flow_model_options_unsupported"},
		{name: "wrong aspect", change: func(s *flowModelSelection) { s.aspect = 2 }, code: "flow_model_options_unsupported"},
		{name: "wrong explicit key", change: func(s *flowModelSelection) { s.key = "frames" }, code: "flow_model_options_unsupported"},
		{name: "seven images", change: func(s *flowModelSelection) { s.inputs = []int{1, 6}; s.images = 7 }, key: "references"},
		{name: "too many images", change: func(s *flowModelSelection) { s.inputs = []int{1, 6}; s.images = 8 }, code: "flow_too_many_references"},
		{name: "too much audio", change: func(s *flowModelSelection) { s.inputs = []int{1, 6, 18}; s.audio = 6 }, code: "flow_too_many_references"},
		{name: "unsupported edit", change: func(s *flowModelSelection) { s.inputs = []int{1, 6, 20} }, code: "flow_model_options_unsupported"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			selection := flowModelSelection{family: "abra", video: true, tier: 3, aspect: 1, duration: 4, resolution: 1, inputs: []int{1}}
			if scenario.change != nil {
				scenario.change(&selection)
			}

			// When: exactly the caller's constraints are selected.
			got, err := selectFlowModel(usages, selection)

			// Then: incompatible keys cannot silently replace those constraints.
			if scenario.code != "" {
				if safeCredentialCode(err) != scenario.code {
					t.Fatalf("error = %v, want %s", err, scenario.code)
				}
			} else if err != nil || got.key != scenario.key {
				t.Fatalf("selected = %s, error = %v", got.key, err)
			}
		})
	}
}
