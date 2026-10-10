package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

var flowOutNow = time.Now

// flowExecutionResult serializes the Gemini-shaped Flow media body into the
// protocol the host selected (request.Format). Every candidate and every
// inlineData part is carried through; usage is never reported because Flow
// does not meter tokens. Chat chunks are bare JSON (the host adds "data:" and
// the terminal [DONE]); Responses and Claude chunks are complete SSE frames.
func flowExecutionResult(body []byte, format string, stream bool) (interface{}, error) {
	var build func(flowOutReply, bool) *flowOutFrames
	switch format {
	case "gemini":
		return webExecutionResult(body, stream), nil
	case "openai":
		build = flowOutChat
	case "openai-response":
		build = flowOutResponses
	case "claude":
		build = flowOutClaude
	default:
		return nil, failure(400, "unsupported_execution_format")
	}
	reply, err := flowOutParse(body)
	if err != nil {
		return nil, err
	}
	frames := build(reply, stream)
	if frames.err != nil {
		return nil, frames.err
	}
	if !stream {
		return webExecutionResult(frames.list[0], false), nil
	}
	chunks := make([]flowOutChunk, len(frames.list))
	for i, frame := range frames.list {
		chunks[i] = flowOutChunk{frame}
	}
	return flowOutStream{http.Header{"Content-Type": {"text/event-stream"}}, chunks}, nil
}

type flowOutChunk struct{ Payload []byte }
type flowOutStream struct {
	Headers http.Header    `json:"headers"`
	Chunks  []flowOutChunk `json:"chunks"`
}

type flowOutFrames struct {
	list [][]byte
	err  error
}

func (frames *flowOutFrames) add(event string, value any) {
	if frames.err != nil {
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		frames.err = failure(500, "flow_response_encoding_failed")
		return
	}
	if event != "" {
		encoded = []byte("event: " + event + "\ndata: " + string(encoded) + "\n\n")
	}
	frames.list = append(frames.list, encoded)
}

type flowOutPart struct{ text, mime, data string }

func (part flowOutPart) isImage() bool { return strings.HasPrefix(part.mime, "image/") }

func (part flowOutPart) content() string {
	if part.data == "" {
		return part.text
	}
	return "data:" + part.mime + ";base64," + part.data
}

type flowOutCandidate struct {
	parts  []flowOutPart
	finish string
}
type flowOutReply struct {
	id, model  string
	created    int64
	candidates []flowOutCandidate
	media      []flowOutMediaReference
}

type flowOutMediaReference struct {
	MediaID   string `json:"mediaId"`
	ProjectID string `json:"projectId"`
	Candidate int    `json:"candidateIndex"`
	Part      int    `json:"partIndex"`
}

type flowOutGemini struct {
	ModelVersion string `json:"modelVersion"`
	Candidates   []struct {
		FinishReason string `json:"finishReason"`
		Content      struct {
			Parts []struct {
				Text       string                 `json:"text"`
				InlineData *webInlinePart         `json:"inlineData"`
				Flow       *flowOutMediaReference `json:"flow"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

func flowOutParse(body []byte) (flowOutReply, error) {
	var parsed flowOutGemini
	if err := json.Unmarshal(body, &parsed); err != nil {
		return flowOutReply{}, failure(502, "flow_response_invalid")
	}
	digest := sha256.Sum256(body)
	reply := flowOutReply{id: hex.EncodeToString(digest[:12]), model: parsed.ModelVersion, created: flowOutNow().Unix()}
	total := 0
	for candidateIndex, candidate := range parsed.Candidates {
		out := flowOutCandidate{finish: candidate.FinishReason}
		for partIndex, part := range candidate.Content.Parts {
			if part.Flow != nil {
				reference := *part.Flow
				reference.Candidate, reference.Part = candidateIndex, partIndex
				reply.media = append(reply.media, reference)
			}
			switch {
			case part.InlineData != nil:
				if part.InlineData.mimeType() == "" || part.InlineData.Data == "" {
					return flowOutReply{}, failure(502, "flow_response_media_invalid")
				}
				out.parts = append(out.parts, flowOutPart{mime: part.InlineData.mimeType(), data: part.InlineData.Data})
			case part.Text != "":
				out.parts = append(out.parts, flowOutPart{text: part.Text})
			}
		}
		total += len(out.parts)
		reply.candidates = append(reply.candidates, out)
	}
	if total == 0 {
		return flowOutReply{}, failure(502, "flow_response_empty")
	}
	return reply, nil
}

func ptr[T any](value T) *T { return &value }
