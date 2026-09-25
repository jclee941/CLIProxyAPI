package logging

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
)

func TestFileRequestLogger_LogRequest_ErrorNamingWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	logger := NewFileRequestLogger(true, dir, "", 10)

	// 1. Non-streaming failure (status 500) -> must produce error-* filename
	errLogReq := logger.LogRequest(
		"/v1/chat/completions",
		http.MethodPost,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"input":"fail"}`),
		http.StatusInternalServerError,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"error":"server error"}`),
		nil, nil, nil, nil, nil,
		"req-fail-500",
		time.Now(),
		time.Now(),
	)
	if errLogReq != nil {
		t.Fatalf("LogRequest failed: %v", errLogReq)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	var found500 string
	for _, e := range entries {
		if strings.Contains(e.Name(), "req-fail-500") {
			found500 = e.Name()
			break
		}
	}
	if found500 == "" {
		t.Fatal("req-fail-500 log file not found")
	}
	if !strings.HasPrefix(found500, "error-") {
		t.Fatalf("expected error- prefix for failed request, got: %s", found500)
	}

	// 2. Non-streaming retry success (status 200, apiResponseErrors present) -> must NOT have error- prefix
	retryErr := &interfaces.ErrorMessage{StatusCode: http.StatusTooManyRequests, Error: fmt.Errorf("rate limited")}
	errLogRetry := logger.LogRequest(
		"/v1/chat/completions",
		http.MethodPost,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"input":"retry"}`),
		http.StatusOK,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"choices":[]}`),
		nil, nil, nil, nil,
		[]*interfaces.ErrorMessage{retryErr},
		"req-retry-200",
		time.Now(),
		time.Now(),
	)
	if errLogRetry != nil {
		t.Fatalf("LogRequest failed: %v", errLogRetry)
	}

	entries, _ = os.ReadDir(dir)
	var foundRetry string
	for _, e := range entries {
		if strings.Contains(e.Name(), "req-retry-200") {
			foundRetry = e.Name()
			break
		}
	}
	if foundRetry == "" {
		t.Fatal("req-retry-200 log file not found")
	}
	if strings.HasPrefix(foundRetry, "error-") {
		t.Fatalf("successful retry must NOT have error- prefix, got: %s", foundRetry)
	}

	// 3. Non-streaming success (status 200, no errors) -> must NOT have error- prefix
	errLogOk := logger.LogRequest(
		"/v1/chat/completions",
		http.MethodPost,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"input":"ok"}`),
		http.StatusOK,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"choices":[]}`),
		nil, nil, nil, nil, nil,
		"req-ok-200",
		time.Now(),
		time.Now(),
	)
	if errLogOk != nil {
		t.Fatalf("LogRequest failed: %v", errLogOk)
	}

	entries, _ = os.ReadDir(dir)
	var foundOk string
	for _, e := range entries {
		if strings.Contains(e.Name(), "req-ok-200") {
			foundOk = e.Name()
			break
		}
	}
	if foundOk == "" {
		t.Fatal("req-ok-200 log file not found")
	}
	if strings.HasPrefix(foundOk, "error-") {
		t.Fatalf("successful request must NOT have error- prefix, got: %s", foundOk)
	}
}

func TestFileRequestLogger_LogStreamingRequest_ErrorNaming(t *testing.T) {
	dir := t.TempDir()
	logger := NewFileRequestLogger(true, dir, "", 10)

	// Case 1: Streaming failure (starts with 200, writes error chunk at end)
	streamWriter, err := logger.LogStreamingRequest(
		"/v1/chat/completions",
		http.MethodPost,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"stream":true}`),
		"stream-err-1",
	)
	if err != nil {
		t.Fatalf("LogStreamingRequest failed: %v", err)
	}

	_ = streamWriter.WriteStatus(http.StatusOK, map[string][]string{"Content-Type": {"text/event-stream"}})
	streamWriter.WriteChunkAsync([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
	streamWriter.WriteChunkAsync([]byte("event: error\ndata: {\"error\":{\"message\":\"stream failed\"}}\n\n"))
	if errClose := streamWriter.Close(); errClose != nil {
		t.Fatalf("stream Close() failed: %v", errClose)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	var foundStreamErr string
	for _, e := range entries {
		if strings.Contains(e.Name(), "stream-err-1") {
			foundStreamErr = e.Name()
			break
		}
	}
	if foundStreamErr == "" {
		t.Fatal("stream-err-1 log file not found")
	}
	if !strings.HasPrefix(foundStreamErr, "error-") {
		t.Fatalf("streaming failure must have error- prefix, got: %s", foundStreamErr)
	}

	// Case 2: Streaming success (starts with 200, ends with [DONE])
	streamWriterOk, errOk := logger.LogStreamingRequest(
		"/v1/chat/completions",
		http.MethodPost,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"stream":true}`),
		"stream-ok-2",
	)
	if errOk != nil {
		t.Fatalf("LogStreamingRequest failed: %v", errOk)
	}

	_ = streamWriterOk.WriteStatus(http.StatusOK, map[string][]string{"Content-Type": {"text/event-stream"}})
	streamWriterOk.WriteChunkAsync([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"))
	streamWriterOk.WriteChunkAsync([]byte("data: [DONE]\n\n"))
	if errClose := streamWriterOk.Close(); errClose != nil {
		t.Fatalf("stream Close() failed: %v", errClose)
	}

	entries, _ = os.ReadDir(dir)
	var foundStreamOk string
	for _, e := range entries {
		if strings.Contains(e.Name(), "stream-ok-2") {
			foundStreamOk = e.Name()
			break
		}
	}
	if foundStreamOk == "" {
		t.Fatal("stream-ok-2 log file not found")
	}
	if strings.HasPrefix(foundStreamOk, "error-") {
		t.Fatalf("streaming success must NOT have error- prefix, got: %s", foundStreamOk)
	}
}

func TestIsRequestErrorLogFile(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name     string
		fileName string
		content  string
		want     bool
	}{
		{
			name:     "explicit error prefix",
			fileName: "error-request-1.log",
			content:  "some log content",
			want:     true,
		},
		{
			name:     "main log",
			fileName: "main.log",
			content:  "app startup error: something failed",
			want:     false,
		},
		{
			name:     "rotated main log",
			fileName: "main.log.1",
			content:  "app log",
			want:     false,
		},
		{
			name:     "hidden file",
			fileName: ".test.log",
			content:  "hidden",
			want:     false,
		},
		{
			name:     "non log extension",
			fileName: "error-request.tmp",
			content:  "temp",
			want:     false,
		},
		{
			name:     "downstream 404 error",
			fileName: "v1-chat-completions-2026-09-22-1.log",
			content:  "=== REQUEST INFO ===\nURL: /v1/models\n=== RESPONSE ===\nStatus: 404\n\n{\"error\":\"not found\"}\n",
			want:     true,
		},
		{
			name:     "downstream 502 bad gateway",
			fileName: "v1-chat-completions-2026-09-22-2.log",
			content:  "=== REQUEST INFO ===\nURL: /v1/chat/completions\n=== RESPONSE ===\nStatus: 502\n\n{\"error\":\"bad gateway\"}\n",
			want:     true,
		},
		{
			name:     "downstream 200 success",
			fileName: "v1-chat-completions-2026-09-22-3.log",
			content:  "=== REQUEST INFO ===\nURL: /v1/chat/completions\n=== RESPONSE ===\nStatus: 200\nContent-Type: application/json\n\n{\"choices\":[]}\n",
			want:     false,
		},
		{
			name:     "upstream retry that succeeded downstream 200",
			fileName: "v1-chat-completions-2026-09-22-4.log",
			content:  "=== REQUEST INFO ===\nURL: /v1/chat/completions\n=== API ERROR RESPONSE ===\nHTTP Status: 429\nrate limit\n=== RESPONSE ===\nStatus: 200\nContent-Type: application/json\n\n{\"choices\":[]}\n",
			want:     false,
		},
		{
			name:     "streaming 200 ending in error",
			fileName: "v1-chat-completions-2026-09-22-5.log",
			content:  "=== REQUEST INFO ===\nURL: /v1/chat/completions\n=== RESPONSE ===\nStatus: 200\nContent-Type: text/event-stream\n\ndata: {\"choices\":[]}\n\nevent: error\ndata: {\"error\":{\"message\":\"fail\"}}\n\n",
			want:     true,
		},
		{
			name:     "streaming 200 ending in success [DONE]",
			fileName: "v1-chat-completions-2026-09-22-6.log",
			content:  "=== REQUEST INFO ===\nURL: /v1/chat/completions\n=== RESPONSE ===\nStatus: 200\nContent-Type: text/event-stream\n\ndata: {\"choices\":[]}\n\ndata: [DONE]\n\n",
			want:     false,
		},
		{
			name:     "massive base64 response 200 success",
			fileName: "v1-images-generations-2026-09-22-7.log",
			content:  "=== REQUEST INFO ===\nURL: /v1/images/generations\n=== RESPONSE ===\nStatus: 200\nContent-Type: application/json\n\n{\"data\":[{\"b64\":\"" + strings.Repeat("B", 1024*1024) + "\"}]}\n",
			want:     false,
		},
		{
			name:     "responses API terminal failure",
			fileName: "v1-responses-failure.log",
			content:  "=== RESPONSE ===\nStatus: 200\nContent-Type: text/event-stream\n\nevent: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\"}}\n\n",
			want:     true,
		},
		{
			name:     "stream error with reordered fields",
			fileName: "v1-responses-reordered.log",
			content:  "=== RESPONSE ===\nStatus: 200\nContent-Type: text/event-stream\n\ndata: {\"id\":\"x\",\"error\":{\"code\":\"failed\"}}\n\n",
			want:     true,
		},
		{
			name:     "successful plain text describing SSE errors",
			fileName: "v1-text-success.log",
			content:  "=== RESPONSE ===\nStatus: 200\nContent-Type: text/plain\n\nExample:\nevent: error\ndata: {\"error\":{\"code\":\"example\"}}\n",
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.fileName)
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("write file: %v", err)
			}
			got := IsRequestErrorLogFile(path)
			if got != tc.want {
				t.Errorf("IsRequestErrorLogFile(%s) = %t, want %t", tc.fileName, got, tc.want)
			}
		})
	}
}
