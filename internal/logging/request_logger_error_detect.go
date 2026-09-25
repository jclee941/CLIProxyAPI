package logging

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/clienterror"
)

// isRequestFailureStatus reports whether a downstream status marks a failed
// request. A client that closed its request (499) cancelled it rather than
// failed it, which is also why it writes no error log while request logging is
// off.
func isRequestFailureStatus(statusCode int) bool {
	return statusCode >= http.StatusBadRequest && statusCode != clienterror.StatusClientClosedRequest
}

// IsRequestErrorLogFile determines whether a given log file represents an actual request failure.
// It instantly recognizes error-* prefixed files without reading disk.
// For un-prefixed request logs, it inspects a bounded tail chunk (at most 64KB) to detect
// actual downstream failures (status >= 400 or streaming HTTP 200 ending in an SSE error),
// while distinguishing successful requests and upstream retries that ultimately succeeded,
// and avoiding scanning massive base64 payloads.
func IsRequestErrorLogFile(filePath string) bool {
	name := filepath.Base(filePath)
	if !strings.HasSuffix(name, ".log") || strings.HasPrefix(name, ".") {
		return false
	}
	if name == "main.log" || strings.HasPrefix(name, "main.log.") {
		return false
	}
	if strings.HasPrefix(name, "error-") {
		return true
	}

	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return false
	}

	const maxTail = 64 * 1024
	readSize := int64(maxTail)
	if info.Size() < readSize {
		readSize = info.Size()
	}
	offset := info.Size() - readSize
	buf := make([]byte, readSize)
	n, err := f.ReadAt(buf, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	tail := buf[:n]

	respIdx := bytes.LastIndex(tail, []byte("=== RESPONSE ===\n"))
	if respIdx != -1 {
		respSection := tail[respIdx:]
		statusCode := extractStatusCodeFromResponseSection(respSection)
		if isRequestFailureStatus(statusCode) {
			return true
		}
		if statusCode == clienterror.StatusClientClosedRequest {
			return false
		}
		if statusCode > 0 && statusCode < http.StatusBadRequest {
			// Downstream received 2xx/3xx. Check if this is a streaming response ending in an error.
			headers, _, _ := bytes.Cut(respSection, []byte("\n\n"))
			if bytes.Contains(bytes.ToLower(headers), []byte("content-type: text/event-stream")) {
				if isSSEErrorPayload(string(respSection)) {
					return true
				}
			}
			// Downstream status is successful and no streaming terminal error.
			// Even if === API ERROR RESPONSE === exists earlier (upstream retry),
			// this request ultimately succeeded.
			return false
		}
	}

	// If === RESPONSE === was not found in the tail chunk:
	// For smaller files entirely within the tail, check if an upstream error was logged
	// without any downstream response (early abort).
	if info.Size() <= readSize {
		if bytes.Contains(tail, []byte("=== API ERROR RESPONSE ===\n")) {
			return true
		}
	}

	// A large historical response without its headers in the tail cannot be
	// classified here. New failures use the filename prefix instead.
	return false
}

// extractStatusCodeFromResponseSection extracts the HTTP status code from a "=== RESPONSE ===" section.
func extractStatusCodeFromResponseSection(section []byte) int {
	scanner := bufio.NewScanner(bytes.NewReader(section))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Status:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Status:"))
			if code, err := strconv.Atoi(val); err == nil {
				return code
			}
		}
	}
	return 0
}

// isSSEErrorPayload checks whether trailing response text contains SSE terminal error evidence.
func isSSEErrorPayload(payload string) bool {
	lines := strings.Split(payload, "\n")
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if event, found := strings.CutPrefix(line, "event:"); found {
			switch strings.TrimSpace(event) {
			case "error", "response.failed", "interaction.failed":
				return true
			}
		}
		if strings.HasPrefix(line, "data:") {
			dataContent := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var event struct {
				Error     json.RawMessage `json:"error"`
				Type      string          `json:"type"`
				EventType string          `json:"event_type"`
			}
			if json.Unmarshal([]byte(dataContent), &event) != nil {
				continue
			}
			if len(event.Error) > 0 && string(event.Error) != "null" {
				return true
			}
			for _, kind := range []string{event.Type, event.EventType} {
				switch kind {
				case "error", "response.failed", "interaction.failed":
					return true
				}
			}
		}
	}
	return false
}

// isStreamingResponseBodyError inspects the tail of a streaming response body temporary file.
func isStreamingResponseBodyError(filePath string) bool {
	if filePath == "" {
		return false
	}
	info, err := os.Stat(filePath)
	if err != nil || info.Size() == 0 {
		return false
	}
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()

	readSize := int64(4096)
	if info.Size() < readSize {
		readSize = info.Size()
	}
	offset := info.Size() - readSize
	buf := make([]byte, readSize)
	n, err := f.ReadAt(buf, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return isSSEErrorPayload(string(buf[:n]))
}
