package main

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

var (
	bootstrapXSRFPattern    = regexp.MustCompile(`"SNlM0e"\s*:\s*("(?:[^"\\]|\\.)*")`)
	bootstrapSessionPattern = regexp.MustCompile(`"FdrFJe"\s*:\s*("(?:[^"\\]|\\.)*")`)
)

func bootstrapValue(pattern *regexp.Regexp, page string) (string, bool) {
	match := pattern.FindStringSubmatch(page)
	if match == nil {
		return "", false
	}
	var value string
	if json.Unmarshal([]byte(match[1]), &value) != nil || value == "" {
		return "", false
	}
	return value, true
}

func batchFrames(raw []byte) ([]any, error) {
	remaining := strings.TrimLeft(strings.TrimPrefix(string(raw), ")]}'"), " \t\r\n")
	var results []any
	for remaining != "" {
		sizeLine, rest, found := strings.Cut(remaining, "\n")
		if !found {
			return nil, failure(502, "flow_response_invalid")
		}
		size, err := strconv.Atoi(strings.TrimSpace(sizeLine))
		if err != nil {
			return nil, failure(502, "flow_response_invalid")
		}
		frameData, tail, _ := strings.Cut(rest, "\n")
		if units := len(utf16.Encode([]rune(frameData))); size != units && size != units+2 {
			return nil, failure(502, "flow_response_invalid")
		}
		var frames []any
		if json.Unmarshal([]byte(frameData), &frames) != nil {
			return nil, failure(502, "flow_response_invalid")
		}
		results = append(results, frames...)
		remaining = strings.TrimLeft(tail, " \t\r\n")
	}
	return results, nil
}

func jsonField(value any, path ...int) any {
	for _, index := range path {
		list, ok := value.([]any)
		if !ok || index < 0 || index >= len(list) {
			return nil
		}
		value = list[index]
	}
	return value
}

func jsonInteger(value any) (int, bool) {
	number, ok := value.(float64)
	if !ok || number != float64(int(number)) {
		return 0, false
	}
	return int(number), true
}
