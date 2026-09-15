package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Video generation is asynchronous behind a synchronous-looking call: the first
// submission returns a conversation handle and a placeholder, and the reply is
// re-fetched until a download URL appears. The indexes below address that
// handle; none of them are named on the wire.

const webVideoPollInterval = 10 * time.Second

// webVideoBudget bounds the wait for a video that never becomes ready. Without
// it the loop runs until the caller disconnects, holding the plugin long enough
// for the host's other calls to time out and unload it.
const webVideoBudget = 10 * time.Minute

// webVideoTurnsRPC re-reads the conversation the submission opened.
const webVideoTurnsRPC = "hNvQHb"

// webVideoChipMarker marks the placeholder the reply carries while the video is
// still being produced.
const webVideoChipMarker = "googleusercontent.com/video_gen_chip/"

// webVideoFields turns the text request into a video submission. The overrides
// are what distinguishes a video turn from a text one.
// orientation is the framing, and it sits inside the turn rather than in a slot
// of its own: the generation options hang off the prompt, the video options are
// the first of them, and their fourth member is 1 for landscape and 2 for
// portrait. That member had been pinned to landscape, which is why every video
// came back landscape no matter what the caller asked for.
func webVideoFields(prompt string, mode int, conversationID string, orientation int) []any {
	fields := webGenerationFields(prompt, mode, 0, conversationID)
	fields[0] = []any{prompt, 0, nil, nil, nil, nil, 0, nil, nil,
		[]any{nil, nil, nil, nil, nil, nil, []any{[]any{nil, nil, nil, orientation}}}}
	fields[41] = []any{1}
	fields[45] = nil
	fields[49] = 11
	fields[54] = []any{}
	fields[55] = []any{[]any{16}}
	fields[67] = 0
	fields[68] = 1
	fields[80] = 1
	fields[91] = 0
	fields[96] = 0
	fields[98] = 1
	return fields
}

// webJSPBField reads an index that the encoder may have moved into a trailing
// sparse map rather than leaving a hole in the array.
func webJSPBField(value any, index int) any {
	if direct := jsonField(value, index); direct != nil {
		return direct
	}
	list, ok := value.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	sparse, ok := list[len(list)-1].(map[string]any)
	if !ok {
		return nil
	}
	return sparse[strconv.Itoa(index+1)]
}

type webVideoState struct {
	URL   string
	Ready bool
}

// webParseVideoCandidate decides whether the reply already carries the video, is
// still producing it, or failed.
func webParseVideoCandidate(candidate any) (webVideoState, error) {
	video := webJSPBField(jsonField(candidate, 12), 59)
	if url, ok := jsonField(video, 0, 0, 0, 0, 7, 1).(string); ok && strings.HasPrefix(url, "https://") {
		return webVideoState{URL: url, Ready: true}, nil
	}
	if text, ok := jsonField(candidate, 1, 0).(string); ok && strings.Contains(text, webVideoChipMarker) {
		return webVideoState{}, nil
	}
	return webVideoState{}, failure(422, "no_video_generated")
}

// webResponseFrames decodes every payload frame in a generation stream, which is
// not length-prefixed and so is filtered by shape.
func webResponseFrames(raw []byte) ([]any, error) {
	var bodies []any
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "[") {
			continue
		}
		var entries []any
		if json.Unmarshal([]byte(trimmed), &entries) != nil {
			continue
		}
		for _, entry := range entries {
			encoded, ok := jsonField(entry, 2).(string)
			if !ok || jsonField(entry, 0) != "wrb.fr" {
				continue
			}
			var decoded any
			if json.Unmarshal([]byte(encoded), &decoded) != nil {
				return nil, failure(502, "invalid_upstream_frame")
			}
			bodies = append(bodies, decoded)
		}
	}
	if len(bodies) == 0 {
		return nil, failure(502, "missing_upstream_response")
	}
	return bodies, nil
}

func (session *webSession) submitVideo(ctx context.Context, prompt string, account webAccount, model capability, orientation int) ([]byte, error) {
	if session.xsrf == "" {
		if err := session.bootstrap(ctx); err != nil {
			return nil, err
		}
	}
	conversationID, err := webConversationID()
	if err != nil {
		return nil, err
	}
	fields, err := json.Marshal(webVideoFields(prompt, model.Mode, conversationID, orientation))
	if err != nil {
		return nil, failure(400, "web_request_invalid")
	}
	return session.postGeneration(ctx, string(fields), account, model)
}

func (session *webSession) downloadVideo(ctx context.Context, url string) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, failure(400, "web_request_invalid")
	}
	request.Header = session.headers(time.Now())
	response, err := session.client.Do(request)
	if err != nil {
		return 0, nil, failure(502, "video_download_failed")
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()
	body, err := io.ReadAll(io.LimitReader(response.Body, 512*1024*1024))
	if err != nil {
		return response.StatusCode, nil, failure(502, "video_download_failed")
	}
	return response.StatusCode, body, nil
}

// generateVideo submits the prompt and then re-reads the conversation until the
// download appears. The poll interval matches the bridge it replaces.
func (session *webSession) generateVideo(ctx context.Context, prompt string, account webAccount, model capability, orientation int) ([]byte, error) {
	raw, err := session.submitVideo(ctx, prompt, account, model, orientation)
	if err != nil {
		return nil, err
	}
	frames, err := webResponseFrames(raw)
	if err != nil {
		return nil, err
	}
	conversation, reply, candidate := "", "", any(nil)
	for _, frame := range frames {
		identifier, okConversation := jsonField(frame, 1, 0).(string)
		turn, okReply := jsonField(frame, 1, 1).(string)
		if okConversation && okReply {
			conversation, reply = identifier, turn
		}
		if value := jsonField(frame, 4, 0); value != nil {
			candidate = value
		}
	}
	if conversation == "" || reply == "" || candidate == nil {
		return nil, failure(502, "missing_video_operation")
	}
	state, err := webParseVideoCandidate(candidate)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(webVideoBudget)
	for time.Now().Before(deadline) {
		if state.Ready {
			status, content, downloadErr := session.downloadVideo(ctx, state.URL)
			if downloadErr != nil {
				return nil, downloadErr
			}
			if status == http.StatusOK && len(content) >= 8 && string(content[4:8]) == "ftyp" {
				return content, nil
			}
			if status != http.StatusPartialContent {
				return nil, failure(502, "invalid_video_download")
			}
		}
		select {
		case <-ctx.Done():
			return nil, failure(499, "caller_disconnected")
		case <-time.After(webVideoPollInterval):
		}
		turns, err := session.rpc(ctx, webVideoTurnsRPC, []any{conversation, 1, nil, 1, []any{1}, []any{4}, nil, 1})
		if err != nil {
			return nil, err
		}
		if jsonField(turns, 0, 0, 0, 1) != reply {
			return nil, failure(502, "video_operation_mismatch")
		}
		state, err = webParseVideoCandidate(jsonField(turns, 0, 0, 3, 0, 0))
		if err != nil {
			return nil, err
		}
	}
	return nil, failure(504, "video_not_ready")
}
