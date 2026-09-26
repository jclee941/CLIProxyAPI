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
// The framing travels twice, exactly as the web app sends it. Slot 55 carries
// the ids of the chips the user picked, where 16 is the landscape chip and 17
// the portrait one, and the turn carries the orientation those chips translate
// into: the generation options hang off the prompt, the video options are the
// first of them, and their fourth member is 1 for landscape and 2 for portrait.
// Both were pinned to landscape, which is why every video came back landscape,
// and they have to move together - a portrait orientation under a landscape chip
// leaves the upstream holding the stream open until the budget runs out.
func webVideoFields(prompt string, mode int, conversationID string, framing omniFraming, attachments []webAttachment) []any {
	prompt = webReferenceDeclaration(prompt, attachments)
	fields := webGenerationFields(prompt, mode, 0, conversationID, attachments)
	fields[0] = []any{prompt, 0, nil, webAttachmentSlot(attachments), nil, nil, 0, nil, nil,
		[]any{nil, nil, nil, nil, nil, nil, []any{[]any{nil, nil, nil, framing.orientation}}}}
	// The web serializer does not translate selected video chips into generation
	// options when an uploaded video supplies the source framing. The original
	// selected-chip list still travels separately in slot 55.
	for _, attachment := range attachments {
		if strings.HasPrefix(attachment.MIMEType, "video/") {
			fields[0] = []any{prompt, 0, nil, webAttachmentSlot(attachments), nil, nil, 0}
			break
		}
	}
	fields[41] = []any{1}
	fields[45] = nil
	fields[49] = 11
	fields[54] = []any{}
	fields[55] = []any{[]any{framing.chip}}
	fields[67] = 0
	fields[68] = 1
	fields[80] = 1
	fields[91] = 0
	fields[96] = 0
	fields[98] = 1
	return fields
}

// webRoleDeclared reports whether a prompt already names a media role. The tag
// vocabulary is the product's own prompt syntax, so every spelling of it is left
// alone - including the frame tags, which the reference declaration used to
// overwrite because it only looked for the two tags it injects itself.
func webRoleDeclared(prompt string) bool {
	for _, written := range []string{"<VIDEO_", "<IMAGE_", "<PREVIOUS_VIDEO>", "<FIRST_FRAME>", "<LAST_FRAME>", "[# Sources", "[# References"} {
		if strings.Contains(prompt, written) {
			return true
		}
	}
	return false
}

// webReferenceDeclaration names an attached video, which the video tool requires
// before it will look at one at all: an undeclared video is uploaded, accepted
// and then ignored, and the generation answers no_video_generated. An image
// needs nothing, because the tool already takes one as the starting frame, and a
// caller who wrote their own declaration is left alone. Only the first video is
// named; the docs state that referencing across several videos is unsupported.
//
// It is declared as a reference rather than an edit source. Naming it as the
// source is the truer reading of "here is a video, work from it", and it is what
// the prompt guide documents, but the web product does not appear to edit an
// uploaded video: a source-declared turn was accepted, started nothing that the
// web UI could show, and burned the full ten minute budget before failing.
// Reference-declared turns produced a video every time. This follows what the
// product does rather than what the document says it should.
func webReferenceDeclaration(prompt string, attachments []webAttachment) string {
	// A caller who wrote their own role means it.
	if webRoleDeclared(prompt) {
		return prompt
	}
	// An image needs no declaration: the tool takes one as a starting frame
	// without being asked, measured as a reference image driving the first frame
	// of the result. Declaring it as a reference instead tells the model not to
	// use it as an initial frame, which is the opposite of what already works.
	//
	// A video is ambiguous between an edit source, an extension target and a
	// reference, and an undeclared one is uploaded, accepted and then ignored:
	// the generation answers no_video_generated.
	for _, attachment := range attachments {
		if strings.HasPrefix(attachment.MIMEType, "video/") {
			return "[# References <VIDEO_REF_0>@Video1] " + prompt +
				"\n\nUse the given video as a reference for the video generation."
		}
	}
	return prompt
}

// webTurnSummary is what an operator sees about work already in flight. The
// prompt is cut short because the turn record is bounded and because the point
// is to recognise the request, not to read it back.
func webTurnSummary(prompt string, attachments []webAttachment) string {
	summary := strings.Join(strings.Fields(prompt), " ")
	if runes := []rune(summary); len(runes) > 120 {
		summary = strings.TrimSpace(string(runes[:120])) + "..."
	}
	images, videos, files := 0, 0, 0
	for _, attachment := range attachments {
		switch kind, _, _ := strings.Cut(attachment.MIMEType, "/"); kind {
		case "image":
			images++
		case "video":
			videos++
		default:
			files++
		}
	}
	for _, carried := range []struct {
		count int
		name  string
	}{{images, "image"}, {videos, "video"}, {files, "file"}} {
		if carried.count == 0 {
			continue
		}
		summary += " +" + strconv.Itoa(carried.count) + " " + carried.name
		if carried.count > 1 {
			summary += "s"
		}
	}
	return summary
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
	text, _ := jsonField(candidate, 1, 0).(string)
	if strings.Contains(text, webVideoChipMarker) {
		return webVideoState{}, nil
	}
	// Accounts on the asynchronous video flow close the stream once the video is
	// accepted, and until it is ready the reply carries no text and no chip, only
	// the in-progress status. Read as a refusal, every video those accounts made
	// was reported as no_video_generated while it finished unobserved.
	if jsonField(candidate, 8, 0) == float64(1) {
		return webVideoState{}, nil
	}
	return webVideoState{}, webNoVideo(text)
}

// webNoVideo reports a turn the product answered without a video. What it wrote
// instead is the only account of why - a declined prompt and a spent daily video
// allowance arrive as the same bare code - and it sits in the candidate slot the
// text path already reads. It rides in the message rather than the code because
// the code is a matching key, and it is flattened and bounded because a reply is
// written for a reader, not for a header.
func webNoVideo(text string) error {
	refusal := failure(422, "no_video_generated")
	answer := strings.Join(strings.Fields(text), " ")
	if runes := []rune(answer); len(runes) > 400 {
		answer = strings.TrimSpace(string(runes[:400])) + "..."
	}
	if answer != "" {
		refusal.Message += ": " + answer
	}
	return refusal
}

// webVideoLimitReplies are what the product answers a video turn with when the
// account has no video allowance left. Nothing structured marks that case: the
// turn fails like a declined prompt, and only the prose differs. These match
// the three replies production accounts gave, 161 times in the kept logs:
// "you can make more videos once the limit resets, check usage in settings",
// "I can't make more videos today, but I can find some on the web", and
// "sorry, I can't make more videos today, come back tomorrow". A wording not
// listed here fails the turn exactly as before and holds nothing.
var webVideoLimitReplies = []string{
	"한도가 재설정되는 대로 동영상을 더 생성할 수 있습니다",
	"오늘은 더 이상 영상을 생성해 드릴 수 없",
}

func webVideoLimitReply(text string) bool {
	for _, reply := range webVideoLimitReplies {
		if strings.Contains(text, reply) {
			return true
		}
	}
	return false
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

func (session *webSession) submitVideo(ctx context.Context, prompt string, account webAccount, model capability, framing omniFraming, attachments []webAttachment) ([]byte, error) {
	if session.xsrf == "" {
		if err := session.bootstrap(ctx); err != nil {
			return nil, err
		}
	}
	conversationID, err := webConversationID()
	if err != nil {
		return nil, err
	}
	fields, err := json.Marshal(webVideoFields(prompt, model.Mode, conversationID, framing, attachments))
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
func (session *webSession) generateVideo(ctx context.Context, prompt string, account webAccount, model capability, framing omniFraming, attachments []webAttachment) ([]byte, error) {
	raw, err := session.submitVideo(ctx, prompt, account, model, framing, attachments)
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
