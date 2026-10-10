package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

var (
	flowOutTestJPEG = append([]byte("\xff\xd8\xff\xe0\x00\x10JFIF"), make([]byte, 32)...)
	flowOutTestText = "caption"
)

func flowOutTestBody(t *testing.T) []byte {
	t.Helper()
	b64 := base64.StdEncoding.EncodeToString
	body, err := json.Marshal(map[string]any{
		"modelVersion": "flow-test-model",
		"candidates": []any{
			map[string]any{"index": 0, "finishReason": "STOP", "content": map[string]any{"role": "model", "parts": []any{
				map[string]any{"text": flowOutTestText},
				map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": b64(flowTestPNG)}},
				map[string]any{"inlineData": map[string]any{"mime_type": "image/jpeg", "data": b64(flowOutTestJPEG)}},
			}}},
			map[string]any{"index": 1, "finishReason": "STOP", "content": map[string]any{"role": "model", "parts": []any{
				map[string]any{"inlineData": map[string]any{"mimeType": "video/mp4", "data": b64(flowTestMP4)}},
			}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func flowOutTestClock(t *testing.T) int64 {
	t.Helper()
	fixed := time.Unix(1_700_000_000, 0)
	previous := flowOutNow
	flowOutNow = func() time.Time { return fixed }
	t.Cleanup(func() { flowOutNow = previous })
	return fixed.Unix()
}

func flowOutTestWire(t *testing.T, result interface{}) (payload []byte, chunks [][]byte, headers http.Header) {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Payload []byte
		Chunks  []struct{ Payload []byte }
		Headers http.Header
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range wire.Chunks {
		chunks = append(chunks, chunk.Payload)
	}
	return wire.Payload, chunks, wire.Headers
}

func flowOutTestRun(t *testing.T, format string, stream bool) (payload []byte, chunks [][]byte) {
	t.Helper()
	result, err := flowExecutionResult(flowOutTestBody(t), format, stream)
	if err != nil {
		t.Fatal(err)
	}
	payload, chunks, headers := flowOutTestWire(t, result)
	wantType := "application/json"
	if stream {
		wantType = "text/event-stream"
	}
	if got := headers.Get("Content-Type"); got != wantType {
		t.Fatalf("content type = %q, want %q", got, wantType)
	}
	return payload, chunks
}

func flowOutTestMedia(t *testing.T, url, mime string, want []byte) {
	t.Helper()
	prefix := "data:" + mime + ";base64,"
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("url %.40q lacks prefix %q", url, prefix)
	}
	got, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, prefix))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("media bytes for %s changed (err=%v)", mime, err)
	}
}

func flowOutTestNoUsage(t *testing.T, raw []byte) {
	t.Helper()
	if bytes.Contains(raw, []byte(`"usage"`)) {
		t.Fatalf("unexpected usage in %.200s", raw)
	}
}

type flowOutTestChat struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message *struct {
			Role    string  `json:"role"`
			Content *string `json:"content"`
			Images  []struct {
				Type     string `json:"type"`
				Index    int    `json:"index"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"images"`
		} `json:"message"`
		Delta *struct {
			Role    string  `json:"role"`
			Content *string `json:"content"`
			Images  []struct {
				Index    int `json:"index"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"images"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

func TestFlowExecutionResultChatPlain(t *testing.T) {
	created := flowOutTestClock(t)
	payload, _ := flowOutTestRun(t, "openai", false)
	flowOutTestNoUsage(t, payload)
	var chat flowOutTestChat
	if err := json.Unmarshal(payload, &chat); err != nil {
		t.Fatal(err)
	}
	if chat.Object != "chat.completion" || chat.Model != "flow-test-model" || chat.Created != created || chat.ID == "" || len(chat.Choices) != 2 {
		t.Fatalf("envelope = %+v", chat)
	}
	first, second := chat.Choices[0], chat.Choices[1]
	if first.Index != 0 || first.Message.Role != "assistant" || *first.FinishReason != "stop" || *first.Message.Content != flowOutTestText || len(first.Message.Images) != 2 {
		t.Fatalf("candidate 0 = %+v", first.Message)
	}
	for index, image := range first.Message.Images {
		if image.Type != "image_url" || image.Index != index {
			t.Fatalf("image %d = %+v", index, image)
		}
	}
	flowOutTestMedia(t, first.Message.Images[0].ImageURL.URL, "image/png", flowTestPNG)
	flowOutTestMedia(t, first.Message.Images[1].ImageURL.URL, "image/jpeg", flowOutTestJPEG)
	if second.Index != 1 || len(second.Message.Images) != 0 || second.Message.Content == nil {
		t.Fatalf("candidate 1 = %+v", second.Message)
	}
	flowOutTestMedia(t, *second.Message.Content, "video/mp4", flowTestMP4)
}

func TestFlowExecutionResultChatSSE(t *testing.T) {
	flowOutTestClock(t)
	_, chunks := flowOutTestRun(t, "openai", true)
	if len(chunks) != 4 {
		t.Fatalf("chunks = %d, want content+finish per candidate", len(chunks))
	}
	var parsed []flowOutTestChat
	for _, chunk := range chunks {
		flowOutTestNoUsage(t, chunk)
		if bytes.HasPrefix(chunk, []byte("data:")) || bytes.Contains(chunk, []byte("[DONE]")) {
			t.Fatalf("chat chunk must be bare JSON, got %.60q", chunk)
		}
		var chat flowOutTestChat
		if err := json.Unmarshal(chunk, &chat); err != nil {
			t.Fatal(err)
		}
		if chat.Object != "chat.completion.chunk" || chat.Model != "flow-test-model" || chat.ID == "" || len(chat.Choices) != 1 {
			t.Fatalf("chunk = %+v", chat)
		}
		parsed = append(parsed, chat)
		if chat.ID != parsed[0].ID {
			t.Fatalf("chunk id %q differs from %q", chat.ID, parsed[0].ID)
		}
	}
	for position, want := range []int{0, 0, 1, 1} {
		if parsed[position].Choices[0].Index != want {
			t.Fatalf("chunk %d choice index = %d, want %d", position, parsed[position].Choices[0].Index, want)
		}
	}
	content0, finish0, content1, finish1 := parsed[0].Choices[0], parsed[1].Choices[0], parsed[2].Choices[0], parsed[3].Choices[0]
	if content0.FinishReason != nil || content1.FinishReason != nil {
		t.Fatal("content chunks must not carry finish_reason")
	}
	if *finish0.FinishReason != "stop" || *finish1.FinishReason != "stop" || finish0.Delta.Content != nil || len(finish0.Delta.Images) != 0 {
		t.Fatalf("finish chunks = %+v / %+v", finish0, finish1)
	}
	if content0.Delta.Role != "assistant" || *content0.Delta.Content != flowOutTestText || len(content0.Delta.Images) != 2 {
		t.Fatalf("delta 0 = %+v", content0.Delta)
	}
	flowOutTestMedia(t, content0.Delta.Images[0].ImageURL.URL, "image/png", flowTestPNG)
	flowOutTestMedia(t, content0.Delta.Images[1].ImageURL.URL, "image/jpeg", flowOutTestJPEG)
	flowOutTestMedia(t, *content1.Delta.Content, "video/mp4", flowTestMP4)
}

type flowOutTestResponse struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	CreatedAt int64  `json:"created_at"`
	Status    string `json:"status"`
	Model     string `json:"model"`
	Output    []struct {
		ID           string `json:"id"`
		Type         string `json:"type"`
		Status       string `json:"status"`
		Role         string `json:"role"`
		Result       string `json:"result"`
		OutputFormat string `json:"output_format"`
		Content      []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func TestFlowExecutionResultResponsesPlain(t *testing.T) {
	created := flowOutTestClock(t)
	payload, _ := flowOutTestRun(t, "openai-response", false)
	flowOutTestNoUsage(t, payload)
	var response flowOutTestResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if response.Object != "response" || response.Status != "completed" || response.Model != "flow-test-model" || response.CreatedAt != created || response.ID == "" {
		t.Fatalf("envelope = %+v", response)
	}
	wantTypes := []string{"message", "image_generation_call", "image_generation_call", "message"}
	if len(response.Output) != len(wantTypes) {
		t.Fatalf("output items = %d, want %d", len(response.Output), len(wantTypes))
	}
	ids := map[string]bool{}
	for index, item := range response.Output {
		if item.Type != wantTypes[index] || item.Status != "completed" || ids[item.ID] {
			t.Fatalf("item %d = %+v", index, item)
		}
		ids[item.ID] = true
	}
	if got := response.Output[0].Content; len(got) != 1 || got[0].Type != "output_text" || got[0].Text != flowOutTestText {
		t.Fatalf("caption message = %+v", got)
	}
	for position, want := range []struct {
		format string
		bytes  []byte
	}{{"png", flowTestPNG}, {"jpeg", flowOutTestJPEG}} {
		item := response.Output[1+position]
		decoded, err := base64.StdEncoding.DecodeString(item.Result)
		if err != nil || !bytes.Equal(decoded, want.bytes) || item.OutputFormat != want.format {
			t.Fatalf("image %d format=%q err=%v bytes preserved=%v", position, item.OutputFormat, err, bytes.Equal(decoded, want.bytes))
		}
	}
	video := response.Output[3].Content
	if len(video) != 1 || video[0].Type != "output_text" {
		t.Fatalf("video message = %+v", video)
	}
	flowOutTestMedia(t, video[0].Text, "video/mp4", flowTestMP4)
}

func flowOutTestFrames(t *testing.T, chunks [][]byte) (events []string, data []json.RawMessage) {
	t.Helper()
	for _, chunk := range chunks {
		text := string(chunk)
		lines := strings.Split(strings.TrimSuffix(text, "\n\n"), "\n")
		if !strings.HasSuffix(text, "\n\n") || len(lines) != 2 || !strings.HasPrefix(lines[0], "event: ") || !strings.HasPrefix(lines[1], "data: ") {
			t.Fatalf("malformed SSE frame %.80q", text)
		}
		var typed struct {
			Type string `json:"type"`
		}
		raw := json.RawMessage(strings.TrimPrefix(lines[1], "data: "))
		if err := json.Unmarshal(raw, &typed); err != nil || typed.Type != strings.TrimPrefix(lines[0], "event: ") {
			t.Fatalf("event %q does not match payload type %q (err=%v)", lines[0], typed.Type, err)
		}
		events = append(events, typed.Type)
		data = append(data, raw)
	}
	return events, data
}

func TestFlowExecutionResultResponsesSSE(t *testing.T) {
	flowOutTestClock(t)
	plain, _ := flowOutTestRun(t, "openai-response", false)
	_, chunks := flowOutTestRun(t, "openai-response", true)
	events, data := flowOutTestFrames(t, chunks)
	message := []string{"response.output_item.added", "response.content_part.added", "response.output_text.delta", "response.output_text.done", "response.content_part.done", "response.output_item.done"}
	image := []string{"response.output_item.added", "response.image_generation_call.completed", "response.output_item.done"}
	want := []string{"response.created", "response.in_progress"}
	for _, group := range [][]string{message, image, image, message} {
		want = append(want, group...)
	}
	want = append(want, "response.completed")
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("lifecycle = %v\nwant %v", events, want)
	}

	var items []int
	for index, raw := range data {
		flowOutTestNoUsage(t, raw)
		var event struct {
			Sequence    *int            `json:"sequence_number"`
			OutputIndex *int            `json:"output_index"`
			Item        json.RawMessage `json:"item"`
		}
		if err := json.Unmarshal(raw, &event); err != nil || event.Sequence == nil || *event.Sequence != index {
			t.Fatalf("frame %d sequence_number = %v (err=%v)", index, event.Sequence, err)
		}
		if events[index] == "response.output_item.added" {
			items = append(items, *event.OutputIndex)
			if bytes.Contains(event.Item, []byte(`"result"`)) || !bytes.Contains(event.Item, []byte(`"in_progress"`)) {
				t.Fatalf("added item must be in progress without media: %.120s", event.Item)
			}
		}
	}
	if fmt.Sprint(items) != "[0 1 2 3]" {
		t.Fatalf("output indices = %v", items)
	}

	var final, streamed struct {
		Response struct {
			Status string          `json:"status"`
			Output json.RawMessage `json:"output"`
		} `json:"response"`
	}
	var plainResponse struct {
		Output json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(data[len(data)-1], &final); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data[0], &streamed); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(plain, &plainResponse); err != nil {
		t.Fatal(err)
	}
	if final.Response.Status != "completed" || streamed.Response.Status != "in_progress" || string(streamed.Response.Output) != "[]" {
		t.Fatalf("status completed=%q created=%q output=%s", final.Response.Status, streamed.Response.Status, streamed.Response.Output)
	}
	if !bytes.Equal(final.Response.Output, plainResponse.Output) {
		t.Fatal("completed event output differs from the plain response output")
	}
	var done struct {
		Item struct {
			Result string `json:"result"`
		} `json:"item"`
	}
	imageDone := 2 + len(message) + 2
	if err := json.Unmarshal(data[imageDone], &done); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(done.Item.Result)
	if events[imageDone] != "response.output_item.done" || err != nil || !bytes.Equal(decoded, flowTestPNG) {
		t.Fatalf("image done frame %q lost bytes (err=%v)", events[imageDone], err)
	}
	var delta struct {
		Delta string `json:"delta"`
		Text  string `json:"text"`
	}
	videoDelta := len(events) - 1 - 3
	if err := json.Unmarshal(data[videoDelta], &delta); err != nil || events[videoDelta] != "response.output_text.done" {
		t.Fatalf("video frame %q (err=%v)", events[videoDelta], err)
	}
	flowOutTestMedia(t, delta.Text, "video/mp4", flowTestMP4)
}

type flowOutTestClaude struct {
	ID           string  `json:"id"`
	Type         string  `json:"type"`
	Role         string  `json:"role"`
	Model        string  `json:"model"`
	StopReason   *string `json:"stop_reason"`
	StopSequence *string `json:"stop_sequence"`
	Content      []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func TestFlowExecutionResultClaudePlain(t *testing.T) {
	flowOutTestClock(t)
	payload, _ := flowOutTestRun(t, "claude", false)
	var message flowOutTestClaude
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatal(err)
	}
	if message.Type != "message" || message.Role != "assistant" || message.Model != "flow-test-model" || message.ID == "" || message.StopReason == nil || *message.StopReason != "end_turn" || len(message.Content) != 4 {
		t.Fatalf("message = %+v", message)
	}
	if message.Usage.InputTokens != 0 || message.Usage.OutputTokens != 0 {
		t.Fatalf("usage must stay zero, got %+v", message.Usage)
	}
	if message.Content[0].Text != flowOutTestText {
		t.Fatalf("text block = %q", message.Content[0].Text)
	}
	flowOutTestMedia(t, message.Content[1].Text, "image/png", flowTestPNG)
	flowOutTestMedia(t, message.Content[2].Text, "image/jpeg", flowOutTestJPEG)
	flowOutTestMedia(t, message.Content[3].Text, "video/mp4", flowTestMP4)
	for _, block := range message.Content {
		if block.Type != "text" {
			t.Fatalf("block type = %q, want text", block.Type)
		}
	}
}

func TestFlowExecutionResultClaudeSSE(t *testing.T) {
	flowOutTestClock(t)
	_, chunks := flowOutTestRun(t, "claude", true)
	events, data := flowOutTestFrames(t, chunks)
	want := []string{"message_start"}
	for range 4 {
		want = append(want, "content_block_start", "content_block_delta", "content_block_stop")
	}
	want = append(want, "message_delta", "message_stop")
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("lifecycle = %v\nwant %v", events, want)
	}
	var start struct {
		Message flowOutTestClaude `json:"message"`
	}
	if err := json.Unmarshal(data[0], &start); err != nil || start.Message.StopReason != nil || len(start.Message.Content) != 0 || start.Message.Model != "flow-test-model" {
		t.Fatalf("message_start = %s (err=%v)", data[0], err)
	}
	wantMedia := []struct {
		mime  string
		bytes []byte
	}{{"image/png", flowTestPNG}, {"image/jpeg", flowOutTestJPEG}, {"video/mp4", flowTestMP4}}
	for block := 0; block < 4; block++ {
		var index struct {
			Index int `json:"index"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		for offset := 1; offset <= 3; offset++ {
			if err := json.Unmarshal(data[1+block*3+offset-1], &index); err != nil || index.Index != block {
				t.Fatalf("block %d frame %d index = %d (err=%v)", block, offset, index.Index, err)
			}
		}
		if err := json.Unmarshal(data[1+block*3+1], &index); err != nil || index.Delta.Type != "text_delta" {
			t.Fatalf("block %d delta = %+v (err=%v)", block, index.Delta, err)
		}
		if block == 0 {
			if index.Delta.Text != flowOutTestText {
				t.Fatalf("text delta = %q", index.Delta.Text)
			}
			continue
		}
		flowOutTestMedia(t, index.Delta.Text, wantMedia[block-1].mime, wantMedia[block-1].bytes)
	}
	var last struct {
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data[len(data)-2], &last); err != nil || last.Delta.StopReason != "end_turn" || last.Usage.OutputTokens != 0 || last.Usage.InputTokens != 0 {
		t.Fatalf("message_delta = %s (err=%v)", data[len(data)-2], err)
	}
}

func TestFlowExecutionResultGeminiUnchanged(t *testing.T) {
	body := flowOutTestBody(t)
	for _, stream := range []bool{false, true} {
		got, err := flowExecutionResult(body, "gemini", stream)
		if err != nil {
			t.Fatal(err)
		}
		gotPayload, gotChunks, _ := flowOutTestWire(t, got)
		wantPayload, wantChunks, _ := flowOutTestWire(t, webExecutionResult(body, stream))
		if !bytes.Equal(gotPayload, wantPayload) || len(gotChunks) != len(wantChunks) || len(gotChunks) > 0 && !bytes.Equal(gotChunks[0], wantChunks[0]) {
			t.Fatalf("gemini result differs from webExecutionResult (stream=%v)", stream)
		}
	}
}

func TestFlowExecutionResultDeterministic(t *testing.T) {
	flowOutTestClock(t)
	for _, format := range []string{"openai", "openai-response", "claude"} {
		for _, stream := range []bool{false, true} {
			firstPayload, firstChunks := flowOutTestRun(t, format, stream)
			secondPayload, secondChunks := flowOutTestRun(t, format, stream)
			if !bytes.Equal(firstPayload, secondPayload) || !bytes.Equal(bytes.Join(firstChunks, nil), bytes.Join(secondChunks, nil)) {
				t.Fatalf("%s stream=%v is not deterministic", format, stream)
			}
		}
	}
}

func TestFlowExecutionResultRejectsBadInput(t *testing.T) {
	media := func(inline string) []byte {
		return []byte(`{"candidates":[{"content":{"parts":[{"inlineData":` + inline + `}]}}]}`)
	}
	cases := map[string]struct {
		body   []byte
		format string
		code   string
	}{
		"unknown format": {flowOutTestBody(t), "codex", "unsupported_execution_format"},
		"not json":       {[]byte("not json"), "openai", "flow_response_invalid"},
		"no candidates":  {[]byte(`{"candidates":[]}`), "openai-response", "flow_response_empty"},
		"empty parts":    {[]byte(`{"candidates":[{"content":{"parts":[]}}]}`), "claude", "flow_response_empty"},
		"empty data":     {media(`{"mimeType":"image/png","data":""}`), "openai", "flow_response_media_invalid"},
		"missing mime":   {media(`{"data":"AAAA"}`), "claude", "flow_response_media_invalid"},
	}
	for name, test := range cases {
		for _, stream := range []bool{false, true} {
			_, err := flowExecutionResult(test.body, test.format, stream)
			var public *publicError
			if !errors.As(err, &public) || public.Code != test.code {
				t.Fatalf("%s stream=%v: err = %v, want code %s", name, stream, err, test.code)
			}
		}
	}
}
