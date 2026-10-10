package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Flow's web SDK buffers one GenerateContent RPC (agJzFb) per text call. The
// model is the server flag the web reads (flag 45843088, `a.ka.ha.Ya`), whose
// bundled default is below; the web falls back to gemini-3-flash-preview only
// when the flag is empty.
const (
	flowTextRPC       = "agJzFb"
	flowTextAction    = "TEXT_GENERATION"
	flowTextModel     = "gemini-3.8-flash-cheap"
	flowTextMaxPrompt = 4000
)

// flowTextThinking maps the SDK's thinkingLevel to the wire enum.
var flowTextThinking = map[string]int{"low": 2, "medium": 3, "high": 4}

type flowTextBlob struct {
	Base64   *string `json:"base64"`
	MimeType *string `json:"mimeType"`
}

type flowTextInput struct {
	Prompt            *string        `json:"prompt"`
	SystemInstruction *string        `json:"systemInstruction"`
	ThinkingLevel     *string        `json:"thinkingLevel"`
	Images            []flowTextBlob `json:"images"`
	Videos            []flowTextBlob `json:"videos"`
	Audios            []flowTextBlob `json:"audios"`
}

// flowTextRequest is the validated input in wire form.
type flowTextRequest struct {
	system   string
	thinking int
	parts    []any
}

// flowJSTrim trims exactly the characters JavaScript's String.prototype.trim does.
func flowJSTrim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r == 0xFEFF || r != 0x85 && unicode.IsSpace(r)
	})
}

func flowTextUnits(value string) int {
	units := 0
	for _, r := range value {
		units += utf16.RuneLen(r)
	}
	return units
}

func flowTextBlobParts(blobs []flowTextBlob, defaultMIME string) ([]any, error) {
	parts := make([]any, 0, len(blobs))
	for _, blob := range blobs {
		if blob.Base64 == nil {
			return nil, failure(400, "flow_text_media_invalid")
		}
		decoded, err := base64.StdEncoding.DecodeString(*blob.Base64)
		if err != nil {
			decoded, err = base64.URLEncoding.DecodeString(*blob.Base64)
		}
		if err != nil || len(decoded) == 0 {
			return nil, failure(400, "flow_text_media_invalid")
		}
		mimeType := defaultMIME
		if blob.MimeType != nil {
			if trimmed := flowJSTrim(*blob.MimeType); trimmed != "" {
				mimeType = trimmed
			}
		}
		if !utf8.ValidString(mimeType) || len(mimeType) > 255 || strings.ContainsFunc(mimeType, unicode.IsControl) {
			return nil, failure(400, "flow_text_media_invalid")
		}
		// Part union: text is field 1, inline blob (mime, base64 data) field 2.
		parts = append(parts, []any{nil, []any{mimeType, *blob.Base64}})
	}
	return parts, nil
}

func parseFlowText(body []byte) (flowTextRequest, error) {
	var input flowTextInput
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		return flowTextRequest{}, failure(400, "flow_text_request_invalid")
	}
	if input.Prompt == nil || !utf8.ValidString(*input.Prompt) {
		return flowTextRequest{}, failure(400, "flow_text_prompt_invalid")
	}
	prompt := flowJSTrim(*input.Prompt)
	if prompt == "" || flowTextUnits(prompt) > flowTextMaxPrompt {
		return flowTextRequest{}, failure(400, "flow_text_prompt_invalid")
	}
	request := flowTextRequest{parts: []any{[]any{prompt}}}
	if input.SystemInstruction != nil {
		if !utf8.ValidString(*input.SystemInstruction) {
			return flowTextRequest{}, failure(400, "flow_text_system_instruction_invalid")
		}
		request.system = flowJSTrim(*input.SystemInstruction)
	}
	if input.ThinkingLevel != nil {
		level, known := flowTextThinking[*input.ThinkingLevel]
		if !known {
			return flowTextRequest{}, failure(400, "flow_text_thinking_level_invalid")
		}
		request.thinking = level
	}
	for _, group := range []struct {
		blobs []flowTextBlob
		mime  string
	}{{input.Images, "image/png"}, {input.Videos, "video/mp4"}, {input.Audios, "audio/wav"}} {
		parts, err := flowTextBlobParts(group.blobs, group.mime)
		if err != nil {
			return flowTextRequest{}, err
		}
		request.parts = append(request.parts, parts...)
	}
	return request, nil
}

// flowTextArgs is the GenerateContentRequest as a positional array: model is
// field 1, contents field 10, system content 12, thinking 13 and the captcha
// token (token, 1) field 16.
func flowTextArgs(token string, request flowTextRequest) []any {
	args := make([]any, 16)
	args[0] = flowTextModel
	args[9] = []any{[]any{request.parts, "user"}}
	if request.system != "" {
		args[11] = []any{[]any{[]any{request.system}}}
	}
	if request.thinking != 0 {
		args[12] = []any{nil, nil, request.thinking}
	}
	args[15] = []any{token, 1}
	return args
}

// flowTextOutput joins the first candidate's non-thought text parts. A reply
// without any text is a failure, never an empty success.
func flowTextOutput(payload any) (string, error) {
	candidates, ok := jsonField(payload, 3).([]any)
	if !ok || len(candidates) == 0 {
		return "", failure(502, "flow_text_output_missing")
	}
	parts, ok := jsonField(candidates[0], 1, 0).([]any)
	if !ok {
		return "", failure(502, "flow_text_output_missing")
	}
	var text strings.Builder
	for _, part := range parts {
		value := jsonField(part, 0)
		if value == nil || flowFlag(jsonField(part, 4)) {
			continue
		}
		piece, ok := value.(string)
		if !ok {
			return "", failure(502, "flow_response_invalid")
		}
		text.WriteString(piece)
	}
	if text.Len() == 0 {
		return "", failure(502, "flow_text_output_missing")
	}
	return text.String(), nil
}

// flowTextHTTP serves POST /v1/flow/projects/{project}/text:generate.
func (service *service) flowTextHTTP(ctx context.Context, record storageRecord, project string, request flowHTTPRequest) (httpResponse, error) {
	if request.Method != http.MethodPost || !flowUUIDPattern.MatchString(project) {
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	input, err := parseFlowText(request.Body)
	if err != nil {
		return httpResponse{}, err
	}
	var text string
	err = service.flowSubmit(ctx, record, project, flowTextAction, func(token string) (string, any) {
		return flowTextRPC, flowTextArgs(token, input)
	}, func(payload any) error {
		var err error
		text, err = flowTextOutput(payload)
		return err
	})
	if err != nil {
		return httpResponse{}, err
	}
	return flowJSON(http.StatusOK, map[string]string{"text": text, "model": flowTextModel})
}
