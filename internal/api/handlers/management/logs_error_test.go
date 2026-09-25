package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

type errorLogsListResponse struct {
	Files []struct {
		Name     string `json:"name"`
		Size     int64  `json:"size"`
		Modified int64  `json:"modified"`
	} `json:"files"`
}

func performGetRequestErrorLogs(t *testing.T, h *Handler) errorLogsListResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/request-error-logs", nil)
	h.GetRequestErrorLogs(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("GetRequestErrorLogs status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp errorLogsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode GetRequestErrorLogs response: %v", err)
	}
	return resp
}

func performDownloadRequestErrorLog(t *testing.T, h *Handler, name string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "name", Value: name}}
	c.Request = httptest.NewRequest(http.MethodGet, "/request-error-logs/"+name, nil)
	h.DownloadRequestErrorLog(c)
	return rec.Code, rec.Body.String()
}

func performGetRequestLogByID(t *testing.T, h *Handler, id string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Request = httptest.NewRequest(http.MethodGet, "/request-log-by-id/"+id, nil)
	h.GetRequestLogByID(c)
	return rec.Code, rec.Body.String()
}

func TestGetRequestErrorLogs_WhenRequestLogEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()

	cfg := &config.Config{}
	cfg.LoggingToFile = true
	cfg.RequestLog = true // CRITICAL: request-log is true!
	h := NewHandlerWithoutConfigFilePath(cfg, nil)
	h.SetLogDirectory(dir)

	// File 1: An explicit error-prefixed log file
	errLogContent := "=== REQUEST INFO ===\nURL: /v1/chat/completions\nMethod: POST\n=== RESPONSE ===\nStatus: 500\n\n{\"error\":\"internal\"}\n"
	errFileName := "error-v1-chat-completions-2026-09-22T100000-req-err-1.log"
	if err := os.WriteFile(filepath.Join(dir, errFileName), []byte(errLogContent), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// File 2: An existing retained failure log without error- prefix (e.g. downstream 401)
	retainedFailContent := "=== REQUEST INFO ===\nURL: /v1/chat/completions\nMethod: POST\n=== RESPONSE ===\nStatus: 401\n\n{\"error\":\"unauthorized\"}\n"
	retainedFailName := "v1-chat-completions-2026-09-22T100001-req-fail-2.log"
	if err := os.WriteFile(filepath.Join(dir, retainedFailName), []byte(retainedFailContent), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// File 3: A normal successful request log (Status: 200) - MUST BE EXCLUDED
	successContent := "=== REQUEST INFO ===\nURL: /v1/chat/completions\nMethod: POST\n=== RESPONSE ===\nStatus: 200\nContent-Type: application/json\n\n{\"choices\":[]}\n"
	successName := "v1-chat-completions-2026-09-22T100002-req-ok-3.log"
	if err := os.WriteFile(filepath.Join(dir, successName), []byte(successContent), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// File 4: An upstream retry that ultimately SUCCEEDED (Status: 200 downstream) - MUST BE EXCLUDED
	retrySuccessContent := "=== REQUEST INFO ===\nURL: /v1/chat/completions\nMethod: POST\n=== API ERROR RESPONSE ===\nHTTP Status: 429\nrate limit\n=== RESPONSE ===\nStatus: 200\nContent-Type: application/json\n\n{\"choices\":[]}\n"
	retrySuccessName := "v1-chat-completions-2026-09-22T100003-req-retryok-4.log"
	if err := os.WriteFile(filepath.Join(dir, retrySuccessName), []byte(retrySuccessContent), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// File 5: A streaming request ending in an error chunk (Status: 200 initially) - MUST BE INCLUDED
	streamFailContent := "=== REQUEST INFO ===\nURL: /v1/chat/completions\nMethod: POST\n=== RESPONSE ===\nStatus: 200\nContent-Type: text/event-stream\n\ndata: {\"choices\":[]}\n\nevent: error\ndata: {\"error\":{\"message\":\"stream quota exceeded\"}}\n\n"
	streamFailName := "v1-chat-completions-2026-09-22T100004-req-streamfail-5.log"
	if err := os.WriteFile(filepath.Join(dir, streamFailName), []byte(streamFailContent), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// File 6: A streaming request that succeeded (Status: 200, ends in [DONE]) - MUST BE EXCLUDED
	streamOkContent := "=== REQUEST INFO ===\nURL: /v1/chat/completions\nMethod: POST\n=== RESPONSE ===\nStatus: 200\nContent-Type: text/event-stream\n\ndata: {\"choices\":[]}\n\ndata: [DONE]\n\n"
	streamOkName := "v1-chat-completions-2026-09-22T100005-req-streamok-6.log"
	if err := os.WriteFile(filepath.Join(dir, streamOkName), []byte(streamOkContent), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// File 7: A massive base64 image response (Status: 200) - MUST BE EXCLUDED without scanning massive body
	massiveBody := strings.Repeat("A", 2*1024*1024) // 2MB base64
	massiveOkContent := "=== REQUEST INFO ===\nURL: /v1/images/generations\nMethod: POST\n=== RESPONSE ===\nStatus: 200\nContent-Type: application/json\n\n{\"data\":[{\"b64_json\":\"" + massiveBody + "\"}]}\n"
	massiveOkName := "v1-images-generations-2026-09-22T100006-req-massive-7.log"
	if err := os.WriteFile(filepath.Join(dir, massiveOkName), []byte(massiveOkContent), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// File 8: main.log - MUST BE EXCLUDED
	if err := os.WriteFile(filepath.Join(dir, "main.log"), []byte("app startup\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Ensure different modification times for deterministic ordering
	now := time.Now()
	_ = os.Chtimes(filepath.Join(dir, errFileName), now.Add(time.Second), now.Add(time.Second))
	_ = os.Chtimes(filepath.Join(dir, retainedFailName), now.Add(2*time.Second), now.Add(2*time.Second))
	_ = os.Chtimes(filepath.Join(dir, streamFailName), now.Add(3*time.Second), now.Add(3*time.Second))

	// Act: GetRequestErrorLogs
	resp := performGetRequestErrorLogs(t, h)

	// Assert:
	// Only errFileName, retainedFailName, streamFailName should be returned!
	nameSet := make(map[string]bool)
	for _, f := range resp.Files {
		nameSet[f.Name] = true
	}

	if !nameSet[errFileName] {
		t.Errorf("expected %s to be listed in error logs, got: %#v", errFileName, resp.Files)
	}
	if !nameSet[retainedFailName] {
		t.Errorf("expected retained failure %s to be listed in error logs, got: %#v", retainedFailName, resp.Files)
	}
	if !nameSet[streamFailName] {
		t.Errorf("expected streaming failure %s to be listed in error logs, got: %#v", streamFailName, resp.Files)
	}

	// Assert exclusions:
	if nameSet[successName] {
		t.Errorf("normal success %s must NOT be in error logs", successName)
	}
	if nameSet[retrySuccessName] {
		t.Errorf("successful retry %s must NOT be in error logs", retrySuccessName)
	}
	if nameSet[streamOkName] {
		t.Errorf("successful stream %s must NOT be in error logs", streamOkName)
	}
	if nameSet[massiveOkName] {
		t.Errorf("massive success %s must NOT be in error logs", massiveOkName)
	}
	if nameSet["main.log"] {
		t.Errorf("main.log must NOT be in error logs")
	}

	if len(resp.Files) != 3 {
		t.Errorf("expected exactly 3 error log files, got %d: %#v", len(resp.Files), resp.Files)
	}

	// Test DownloadRequestErrorLog
	t.Run("DownloadExplicitErrorLog", func(t *testing.T) {
		status, body := performDownloadRequestErrorLog(t, h, errFileName)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200", status)
		}
		if body != errLogContent {
			t.Fatalf("body = %q, want %q", body, errLogContent)
		}
	})

	t.Run("DownloadRetainedErrorLogWithoutPrefix", func(t *testing.T) {
		status, body := performDownloadRequestErrorLog(t, h, retainedFailName)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200", status)
		}
		if body != retainedFailContent {
			t.Fatalf("body = %q, want %q", body, retainedFailContent)
		}
	})

	t.Run("DownloadRejectsSuccessLog", func(t *testing.T) {
		status, _ := performDownloadRequestErrorLog(t, h, successName)
		if status != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 for success log", status)
		}
	})

	t.Run("GetRequestLogByID_MatchesBoth", func(t *testing.T) {
		// Error file: req-err-1
		status, body := performGetRequestLogByID(t, h, "req-err-1")
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 for req-err-1", status)
		}
		if body != errLogContent {
			t.Fatalf("body mismatch")
		}

		// Success file: req-ok-3
		status, body = performGetRequestLogByID(t, h, "req-ok-3")
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 for req-ok-3", status)
		}
		if body != successContent {
			t.Fatalf("body mismatch")
		}
	})
}
