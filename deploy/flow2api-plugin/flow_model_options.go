package main

import "slices"

// flowModelUsage is a single executable combination advertised by GetModels.
// Field positions follow the current Flow web client's M4a/J4a decoders.
type flowModelUsage struct {
	family, key               string
	video                     bool
	aspects, resolutions      []int
	inputs                    [][]int
	duration                  int
	variableDuration          bool
	prices                    map[int]int
	imageLimit, identityLimit int
	totalLimit, videoSeconds  int
}

func flowIntegers(value any) []int {
	list, _ := value.([]any)
	result := make([]int, 0, len(list))
	for _, item := range list {
		if number, ok := jsonInteger(item); ok {
			result = append(result, number)
		}
	}
	return result
}

func decodeFlowModels(payload any) ([]flowModelUsage, error) {
	var result []flowModelUsage
	for _, group := range []int{4, 5} {
		families, _ := jsonField(payload, 0, group).([]any)
		for _, family := range families {
			familyID, _ := jsonField(family, 3).(string)
			usages, _ := jsonField(family, 1).([]any)
			for _, raw := range usages {
				key, _ := jsonField(raw, 0).(string)
				if familyID == "" || key == "" {
					return nil, failure(502, "flow_model_catalog_invalid")
				}
				usage := flowModelUsage{family: familyID, key: key, video: group == 4, prices: map[int]int{}}
				aspectField, inputField := 13, 8
				if usage.video {
					aspectField, inputField = 12, 7
					usage.duration, _ = jsonInteger(jsonField(raw, 16))
					if usage.duration == 0 {
						usage.duration = 8
					}
					usage.variableDuration, _ = jsonField(raw, 22).(bool)
					usage.resolutions = flowIntegers(jsonField(raw, 23, 0))
					if len(usage.resolutions) == 0 {
						usage.resolutions = []int{1}
					}
				}
				usage.aspects = flowIntegers(jsonField(raw, aspectField, 0))
				combinations, _ := jsonField(raw, inputField, 0).([]any)
				for _, combination := range combinations {
					usage.inputs = append(usage.inputs, flowIntegers(jsonField(combination, 0)))
				}
				prices, _ := jsonField(raw, 4).([]any)
				for _, price := range prices {
					tier, tierOK := jsonInteger(jsonField(price, 0))
					// A present empty price message means zero credits; a
					// missing price message means this tier cannot use it.
					message, allowed := jsonField(price, 1, 0).([]any)
					if tierOK && allowed {
						credits, _ := jsonInteger(jsonField(message, 1))
						usage.prices[tier] = credits
					}
				}
				usage.imageLimit, _ = jsonInteger(jsonField(raw, 21, 0))
				usage.identityLimit, _ = jsonInteger(jsonField(raw, 21, 1))
				usage.totalLimit, _ = jsonInteger(jsonField(raw, 21, 2))
				usage.videoSeconds, _ = jsonInteger(jsonField(raw, 21, 3))
				if !usage.video {
					usage.imageLimit, _ = jsonInteger(jsonField(raw, 9))
				}
				result = append(result, usage)
			}
		}
	}
	if len(result) == 0 {
		return nil, failure(502, "flow_model_catalog_invalid")
	}
	return result, nil
}

type flowModelSelection struct {
	family, key                        string
	video                              bool
	tier, aspect, duration, resolution int
	inputs                             []int
	images, identities, audio          int
}

func selectFlowModel(usages []flowModelUsage, selection flowModelSelection) (flowModelUsage, error) {
	for _, usage := range usages {
		if usage.family != selection.family || usage.video != selection.video {
			continue
		}
		if selection.key != "" && selection.key != usage.key {
			continue
		}
		if _, allowed := usage.prices[selection.tier]; !allowed {
			continue
		}
		if !slices.Contains(usage.aspects, selection.aspect) {
			continue
		}
		if selection.video && (!slices.Contains(usage.resolutions, selection.resolution) ||
			!usage.variableDuration && usage.duration != selection.duration) {
			continue
		}
		matched := false
		for _, combination := range usage.inputs {
			matched = true
			for _, input := range selection.inputs {
				if !slices.Contains(combination, input) {
					matched = false
					break
				}
			}
			// Do not substitute a different operation just because both
			// accept text, or silently drop a requested reference type.
			for _, operation := range []int{3, 4, 5, 6, 7, 14, 15, 16, 20, 21} {
				if slices.Contains(combination, operation) != slices.Contains(selection.inputs, operation) {
					matched = false
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			continue
		}
		if usage.imageLimit > 0 && selection.images > usage.imageLimit ||
			usage.identityLimit > 0 && selection.identities > usage.identityLimit ||
			usage.totalLimit > 0 && selection.images+selection.identities+selection.audio > usage.totalLimit {
			return flowModelUsage{}, failure(400, "flow_too_many_references")
		}
		return usage, nil
	}
	return flowModelUsage{}, failure(400, "flow_model_options_unsupported")
}
