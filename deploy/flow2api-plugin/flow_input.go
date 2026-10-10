package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"maps"
	"slices"
	"strings"
)

const (
	flowMaxCandidates      = 4
	flowMaxSeed            = 2147483647
	flowMaxImageReferences = 10
)

type flowCrop struct{ Top, Left, Bottom, Right float64 }

type flowReference struct {
	mimeType        string
	data            []byte
	MediaID         string
	CropCoordinates *flowCrop
}

// seed is nil unless the caller sent one, so an explicit zero stays distinguishable.
type flowInput struct {
	prompt     string
	references []flowReference
	aspect     string
	seconds    int
	imageSize  string
	count      int
	seed       *int
	resolution string
	options    flowOptions
}

// flowSamplingOptions are settings chat bridges attach to every request. A
// generation has no use for them, and refusing them would refuse every caller
// that comes through the OpenAI format. seed is not one of them: Flow honours
// it for images and upscales.
var flowSamplingOptions = map[string]bool{
	"temperature": true, "topP": true, "topK": true, "maxOutputTokens": true, "responseModalities": true,
	"thinkingConfig": true, "stopSequences": true, "presencePenalty": true, "frequencyPenalty": true,
	"responseMimeType": true,
}

// parseFlowRequest reads the prompt from the last user turn, since a Flow
// generation keeps no conversation, together with the options Flow can honour.
func parseFlowRequest(model flowModel, raw []byte) (flowInput, error) {
	var body struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text            *string         `json:"text"`
				InlineData      *webInlinePart  `json:"inlineData"`
				InlineDataSnake *webInlinePart  `json:"inline_data"`
				FileData        json.RawMessage `json:"fileData"`
				FileDataSnake   json.RawMessage `json:"file_data"`
			} `json:"parts"`
		} `json:"contents"`
		SystemInstruction *struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"systemInstruction"`
		GenerationConfig map[string]json.RawMessage `json:"generationConfig"`
	}
	if len(raw) > 64*1024*1024 || json.Unmarshal(raw, &body) != nil {
		return flowInput{}, failure(400, "flow_request_invalid")
	}
	turn := -1
	for index := len(body.Contents) - 1; index >= 0; index-- {
		if role := body.Contents[index].Role; role == "" || role == "user" {
			turn = index
			break
		}
	}
	input := flowInput{count: 1}
	var texts []string
	if body.SystemInstruction != nil {
		for _, part := range body.SystemInstruction.Parts {
			if strings.TrimSpace(part.Text) != "" {
				texts = append(texts, part.Text)
			}
		}
	}
	if turn >= 0 {
		for _, part := range body.Contents[turn].Parts {
			inline := part.InlineData
			if inline == nil {
				inline = part.InlineDataSnake
			}
			switch {
			case part.Text != nil:
				texts = append(texts, *part.Text)
			case inline != nil:
				reference, err := flowInlineReference(inline.mimeType(), inline.Data)
				if err != nil {
					return flowInput{}, err
				}
				input.references = append(input.references, reference)
			case len(part.FileData) > 0 || len(part.FileDataSnake) > 0:
				return flowInput{}, failure(400, "flow_file_reference_unsupported")
			}
		}
	}
	input.prompt = strings.TrimSpace(strings.Join(texts, "\n"))
	if err := parseFlowConfig(model, body.GenerationConfig, &input); err != nil {
		return flowInput{}, err
	}
	if err := flowResolveOptions(model, &input); err != nil {
		return flowInput{}, err
	}
	return input, flowDefaults(model, &input)
}

func parseFlowConfig(model flowModel, config map[string]json.RawMessage, input *flowInput) error {
	var aspect, imageAspect string
	for _, key := range slices.Sorted(maps.Keys(config)) {
		value := config[key]
		var err error
		switch key {
		case "aspectRatio":
			err = json.Unmarshal(value, &aspect)
		case "durationSeconds":
			if !model.video {
				return flowOptionRejection(key)
			}
			var seconds *int
			if err = json.Unmarshal(value, &seconds); err == nil && seconds != nil {
				if *seconds <= 0 {
					return failure(400, "flow_invalid_duration")
				}
				input.seconds = *seconds
			}
		case "imageConfig":
			if model.video {
				return flowOptionRejection(key)
			}
			var image struct {
				AspectRatio string `json:"aspectRatio"`
				ImageSize   string `json:"imageSize"`
			}
			if err = flowStrict(key, value, &image); err != nil {
				return err
			}
			imageAspect, input.imageSize = image.AspectRatio, strings.ToUpper(image.ImageSize)
		case "candidateCount":
			var count *int
			if json.Unmarshal(value, &count) != nil || count != nil && (*count < 1 || *count > flowMaxCandidates) {
				return failure(400, "flow_invalid_candidate_count")
			}
			if count != nil {
				input.count = *count
			}
		case "seed":
			var seed *int
			if json.Unmarshal(value, &seed) != nil || seed != nil && (*seed < 0 || *seed > flowMaxSeed) {
				return failure(400, "flow_invalid_seed")
			}
			input.seed = seed
		case "resolution":
			if !model.video {
				return flowOptionRejection(key)
			}
			var resolution string
			if err = json.Unmarshal(value, &resolution); err == nil && resolution != "" {
				if input.resolution = flowResolution(resolution); input.resolution == "" {
					return failure(400, "flow_invalid_resolution")
				}
			}
		case "flow":
			if err = parseFlowOptions(model, value, input); err != nil {
				return err
			}
		default:
			if !flowSamplingOptions[key] {
				return flowOptionRejection(key)
			}
		}
		if err != nil {
			return flowOptionRejection(key)
		}
	}
	if aspect != "" && imageAspect != "" && aspect != imageAspect {
		return failure(400, "flow_conflicting_aspect_ratio")
	}
	input.aspect = aspect
	if aspect == "" {
		input.aspect = imageAspect
	}
	return nil
}

func flowStrict(key string, raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			return flowOptionRejection(key + "." + strings.Trim(name, `"`))
		}
		return flowOptionRejection(key)
	}
	return nil
}

func flowInlineReference(mimeType, data string) (flowReference, error) {
	if !strings.HasPrefix(mimeType, "image/") {
		return flowReference{}, failure(400, "flow_reference_type_unsupported")
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(decoded) == 0 {
		return flowReference{}, failure(400, "flow_reference_invalid")
	}
	return flowReference{mimeType: mimeType, data: decoded}, nil
}

func flowResolution(value string) string {
	switch strings.ToLower(value) {
	case "360p":
		return "360p"
	case "720p":
		return "720p"
	case "1080p":
		return "1080p"
	case "4k":
		return "4K"
	}
	return ""
}

func flowOptionRejection(key string) error {
	return &publicError{Code: "flow_unsupported_generation_option", Message: "flow_unsupported_generation_option: " + key, HTTPStatus: 400}
}
