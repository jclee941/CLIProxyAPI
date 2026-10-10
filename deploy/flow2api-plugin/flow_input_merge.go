package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
)

// mergeFlowGenerationConfig carries the caller's generationConfig, which core
// translation may have dropped, from the original request into the translated
// Gemini payload, along with the OpenAI n and seed. A value that differs from
// one already in the payload is refused; neither input is modified.
func mergeFlowGenerationConfig(payload, original []byte) ([]byte, error) {
	var source map[string]json.RawMessage
	if json.Unmarshal(original, &source) != nil || source == nil {
		return payload, nil
	}
	var target map[string]json.RawMessage
	if json.Unmarshal(payload, &target) != nil || target == nil {
		return nil, failure(400, "flow_request_invalid")
	}
	merged, err := flowConfigObject(target["generationConfig"])
	if err != nil {
		return nil, err
	}
	changed := false
	add := func(key string, value json.RawMessage) error {
		if flowJSONNull(value) {
			return nil
		}
		if existing := merged[key]; !flowJSONNull(existing) {
			var left, right any
			if json.Unmarshal(existing, &left) != nil || json.Unmarshal(value, &right) != nil || !reflect.DeepEqual(left, right) {
				return &publicError{Code: "flow_conflicting_generation_option", Message: "flow_conflicting_generation_option: " + key, HTTPStatus: 400}
			}
			return nil
		}
		merged[key] = value
		changed = true
		return nil
	}
	extra, err := flowConfigObject(source["generationConfig"])
	if err != nil {
		return nil, err
	}
	for _, key := range slices.Sorted(maps.Keys(extra)) {
		if err := add(key, extra[key]); err != nil {
			return nil, err
		}
	}
	for _, mapping := range [][2]string{{"n", "candidateCount"}, {"seed", "seed"}} {
		if err := add(mapping[1], source[mapping[0]]); err != nil {
			return nil, err
		}
	}
	if !changed {
		return payload, nil
	}
	config, err := flowMarshal(merged)
	if err != nil {
		return nil, failure(400, "flow_request_invalid")
	}
	target["generationConfig"] = config
	out, err := flowMarshal(target)
	if err != nil {
		return nil, failure(400, "flow_request_invalid")
	}
	return out, nil
}

func flowConfigObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	config := map[string]json.RawMessage{}
	if !flowJSONNull(raw) {
		if err := json.Unmarshal(raw, &config); err != nil {
			return nil, flowOptionRejection("generationConfig")
		}
	}
	return config, nil
}

func flowJSONNull(raw json.RawMessage) bool {
	return len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null"
}

func flowMarshal(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}
