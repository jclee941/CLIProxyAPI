package main

import (
	"context"
	"encoding/binary"
	"net/http"
	"strings"
	"unicode/utf8"
)

type flowVoiceResource struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	PreviewURL  string `json:"previewUrl,omitempty"`
}

func (service *service) flowVoices(ctx context.Context, record storageRecord, project string) ([]flowVoiceResource, error) {
	contents, err := service.flowProjectContents(ctx, record, project)
	if err != nil {
		return nil, err
	}
	voices := []flowVoiceResource{}
	rows, _ := jsonField(contents, 3).([]any)
	for _, row := range rows {
		id, _ := jsonField(row, 0).(string)
		name, _ := jsonField(row, 2).(string)
		description, _ := jsonField(row, 3, 10, 0, 1).(string)
		link, _ := jsonField(row, 3, 10, 0, 3).(string)
		if !flowIdentifier(id) || name == "" {
			return nil, failure(502, "flow_voice_response_invalid")
		}
		voices = append(voices, flowVoiceResource{ID: id, Name: name, Description: description, PreviewURL: link})
	}
	return voices, nil
}

func (service *service) flowVoiceHTTP(ctx context.Context, record storageRecord, project, tail string, request flowHTTPRequest) (httpResponse, error) {
	if tail == "" && request.Method == http.MethodGet {
		voices, err := service.flowVoices(ctx, record, project)
		if err != nil {
			return httpResponse{}, err
		}
		return flowJSON(200, map[string]any{"voices": voices})
	}
	if tail != ":preview" || request.Method != http.MethodPost {
		return httpResponse{}, failure(404, "flow_route_not_found")
	}
	var input struct {
		Text        string `json:"text"`
		VoiceID     string `json:"voiceId"`
		Description string `json:"description"`
	}
	if err := flowStrict("voice_preview", request.Body, &input); err != nil {
		return httpResponse{}, err
	}
	if strings.TrimSpace(input.Text) == "" || !utf8.ValidString(input.Text) || utf8.RuneCountInString(input.Text) > 120 {
		return httpResponse{}, failure(400, "flow_voice_text_invalid")
	}
	voices, err := service.flowVoices(ctx, record, project)
	if err != nil {
		return httpResponse{}, err
	}
	var name string
	for _, voice := range voices {
		if voice.ID == input.VoiceID {
			name = voice.Name
			break
		}
	}
	if name == "" {
		return httpResponse{}, failure(400, "flow_voice_not_found")
	}
	var media flowMediaResource
	err = service.flowSubmit(ctx, record, project, "AUDIO_GENERATION", func(token string) (string, any) {
		item := []any{input.Text, []any{[]any{name, name}}, "gemini_v4s_tts_flow", input.Description, 2}
		return "no0P6", []any{[]any{item}, flowContext(project, token)}
	}, func(payload any) error {
		var err error
		media, err = decodeFlowMedia(jsonField(payload, 0, 0), project)
		return err
	})
	if err != nil {
		return httpResponse{}, err
	}
	return flowJSON(201, media)
}

// Flow voice previews are signed 24 kHz mono, little-endian 16-bit PCM.
// The web wraps the same bytes in a WAV header before playback.
func flowVoiceWAV(pcm []byte) []byte {
	pcm = pcm[:len(pcm)/2*2]
	wav := make([]byte, 44+len(pcm))
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:8], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:20], 16)
	binary.LittleEndian.PutUint16(wav[20:22], 1)
	binary.LittleEndian.PutUint16(wav[22:24], 1)
	binary.LittleEndian.PutUint32(wav[24:28], 24000)
	binary.LittleEndian.PutUint32(wav[28:32], 48000)
	binary.LittleEndian.PutUint16(wav[32:34], 2)
	binary.LittleEndian.PutUint16(wav[34:36], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:44], uint32(len(pcm)))
	copy(wav[44:], pcm)
	return wav
}
