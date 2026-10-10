package main

import (
	"encoding/json"
	"testing"
)

const flowMergePayload = `{"contents":[{"role":"user","parts":[{"text":"a <fox> & more"}]}]}`

func flowMergeConfig(t *testing.T, merged []byte) map[string]json.RawMessage {
	t.Helper()
	var body map[string]json.RawMessage
	if err := json.Unmarshal(merged, &body); err != nil {
		t.Fatal(err)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(body["generationConfig"], &config); err != nil {
		t.Fatalf("generationConfig in %s: %v", merged, err)
	}
	return config
}

func TestMergeFlowGenerationConfigCopiesTheOriginalConfig(t *testing.T) {
	payload, original := []byte(flowMergePayload), []byte(`{"generationConfig":{"candidateCount":2,"seed":0,"flow":{"mode":"text"}},"contents":[]}`)
	payloadBefore, originalBefore := string(payload), string(original)
	merged, err := mergeFlowGenerationConfig(payload, original)
	if err != nil {
		t.Fatal(err)
	}
	config := flowMergeConfig(t, merged)
	if string(config["candidateCount"]) != "2" || string(config["seed"]) != "0" || string(config["flow"]) != `{"mode":"text"}` {
		t.Fatalf("config = %v", config)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(merged, &body); err != nil {
		t.Fatal(err)
	}
	if string(body["contents"]) != `[{"role":"user","parts":[{"text":"a <fox> & more"}]}]` {
		t.Fatalf("contents changed: %s", body["contents"])
	}
	if string(payload) != payloadBefore || string(original) != originalBefore {
		t.Fatal("an input was modified")
	}
	input, err := parseFlowRequest(flowModel{id: "flow-nano-banana-2", imageModel: "BELUGA"}, merged)
	if err != nil || input.count != 2 || input.seed == nil || *input.seed != 0 {
		t.Fatalf("parsed = %+v, %v", input, err)
	}
}

func TestMergeFlowGenerationConfigMapsOpenAI(t *testing.T) {
	merged, err := mergeFlowGenerationConfig([]byte(flowMergePayload), []byte(`{"model":"x","n":3,"seed":0,"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	config := flowMergeConfig(t, merged)
	if string(config["candidateCount"]) != "3" || string(config["seed"]) != "0" || len(config) != 2 {
		t.Fatalf("config = %v", config)
	}
	existing := `{"generationConfig":{"temperature":1},"contents":[]}`
	merged, err = mergeFlowGenerationConfig([]byte(existing), []byte(`{"n":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if config = flowMergeConfig(t, merged); string(config["temperature"]) != "1" || string(config["candidateCount"]) != "2" {
		t.Fatalf("config = %v", config)
	}
}

func TestMergeFlowGenerationConfigKeepsEqualDuplicatesAndNoOps(t *testing.T) {
	for name, test := range map[string]struct{ payload, original string }{
		"equal n":            {`{"generationConfig":{"candidateCount":2},"contents":[]}`, `{"n":2}`},
		"equal number forms": {`{"generationConfig":{"candidateCount":2},"contents":[]}`, `{"n":2.0}`},
		"equal config":       {`{"generationConfig":{"seed":5},"contents":[]}`, `{"generationConfig":{"seed":5}}`},
		"null values":        {flowMergePayload, `{"n":null,"seed":null,"generationConfig":null}`},
		"nothing to carry":   {flowMergePayload, `{"model":"x"}`},
		"empty original":     {flowMergePayload, ``},
		"not an object":      {flowMergePayload, `[1]`},
	} {
		merged, err := mergeFlowGenerationConfig([]byte(test.payload), []byte(test.original))
		if err != nil || string(merged) != test.payload {
			t.Errorf("%s: %s, %v", name, merged, err)
		}
	}
}

func TestMergeFlowGenerationConfigRefusesConflicts(t *testing.T) {
	for name, test := range map[string]struct{ payload, original, code string }{
		"n against payload":     {`{"generationConfig":{"candidateCount":2},"contents":[]}`, `{"n":3}`, "flow_conflicting_generation_option"},
		"seed against payload":  {`{"generationConfig":{"seed":1},"contents":[]}`, `{"seed":2}`, "flow_conflicting_generation_option"},
		"config against n":      {flowMergePayload, `{"n":2,"generationConfig":{"candidateCount":3}}`, "flow_conflicting_generation_option"},
		"config against config": {`{"generationConfig":{"seed":1},"contents":[]}`, `{"generationConfig":{"seed":2}}`, "flow_conflicting_generation_option"},
		"config not an object":  {flowMergePayload, `{"generationConfig":"x"}`, "flow_unsupported_generation_option"},
		"payload not an object": {`{"generationConfig":[1]}`, `{"n":2}`, "flow_unsupported_generation_option"},
		"payload not json":      {`nope`, `{"n":2}`, "flow_request_invalid"},
	} {
		if _, err := mergeFlowGenerationConfig([]byte(test.payload), []byte(test.original)); safeCredentialCode(err) != test.code {
			t.Errorf("%s: %v, want %s", name, err, test.code)
		}
	}
}
