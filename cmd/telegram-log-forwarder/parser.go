package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	log "github.com/sirupsen/logrus"
)

const maxLogSampleBytes = 4 << 20

var (
	errIncompleteLog    = errors.New("incomplete CPA error log")
	statusPattern       = regexp.MustCompile(`(?m)^Status:\s*(\d{3})\s*$`)
	modelPattern        = regexp.MustCompile(`/models/([^/:?]+)`)
	requestIDPattern    = regexp.MustCompile(`-([a-f0-9]{8})\.log$`)
	messagePattern      = regexp.MustCompile(`(?s)"message"\s*:\s*"((?:\\.|[^"\\])*)"`)
	apiKeyPattern       = regexp.MustCompile(`(?i)sk-[a-z0-9_-]{6,}`)
	bearerPattern       = regexp.MustCompile(`(?i)(Bearer\s+)[^\s"']+`)
	jsonSecretPattern   = regexp.MustCompile(`(?i)("(?:api_?key|token|authorization)"\s*:\s*")[^"]+`)
	googleAPIKeyPattern = regexp.MustCompile(`AIza[0-9A-Za-z_-]{20,}`)
	jwtPattern          = regexp.MustCompile(`eyJ[0-9A-Za-z_-]*\.[0-9A-Za-z_-]+\.[0-9A-Za-z_-]+`)
	githubTokenPattern  = regexp.MustCompile(`gh[pousr]_[0-9A-Za-z]{20,}`)
	awsAccessKeyPattern = regexp.MustCompile(`(?:AKIA|ASIA)[A-Z0-9]{16}`)
	namedSecretPattern  = regexp.MustCompile(`(?i)((?:x-api-key|api[_-]?key|access[_-]?token|refresh[_-]?token|authorization|token)\s*[:=]\s*)[^\s,;"']+`)
)

type errorAlert struct {
	Route          string
	Model          string
	API            string
	Status         int
	Message        string
	File           string
	OccurredAt     time.Time
	Environment    string
	Instance       string
	DashboardURL   string
	RequestID      string
	Occurrences24h int
}

type requestEnvelope struct {
	Model string `json:"model"`
}

type responseEnvelope struct {
	Error json.RawMessage `json:"error"`
}

type responseError struct {
	Message string `json:"message"`
}

func parseErrorLog(path string) (errorAlert, error) {
	data, info, errRead := readLogSample(path)
	if errRead != nil {
		return errorAlert{}, fmt.Errorf("read CPA error log: %w", errRead)
	}

	responseIndex := bytes.LastIndex(data, []byte("=== RESPONSE ===\n"))
	if responseIndex < 0 {
		return errorAlert{}, errIncompleteLog
	}
	responseSection := string(data[responseIndex:])
	statusMatch := statusPattern.FindStringSubmatch(responseSection)
	if len(statusMatch) != 2 {
		return errorAlert{}, errIncompleteLog
	}
	status, errStatus := strconv.Atoi(statusMatch[1])
	if errStatus != nil {
		return errorAlert{}, fmt.Errorf("parse response status: %w", errStatus)
	}

	prefix := string(data[:responseIndex])
	route := extractRoute(prefix)
	message := extractErrorMessage(responseSection, status)
	return errorAlert{
		Route:      route,
		Model:      extractModel(prefix, route),
		API:        classifyAPI(route),
		Status:     status,
		Message:    sanitizeMessage(message),
		File:       filepath.Base(path),
		OccurredAt: info.ModTime(),
		RequestID:  extractRequestID(filepath.Base(path)),
	}, nil
}

func readLogSample(path string) ([]byte, os.FileInfo, error) {
	file, errOpen := os.Open(path)
	if errOpen != nil {
		return nil, nil, errOpen
	}
	defer func() {
		if errClose := file.Close(); errClose != nil {
			log.WithError(errClose).Warn("failed to close CPA error log")
		}
	}()

	info, errStat := file.Stat()
	if errStat != nil {
		return nil, nil, errStat
	}
	if info.Size() <= maxLogSampleBytes {
		data, errRead := io.ReadAll(file)
		return data, info, errRead
	}

	head := make([]byte, 64<<10)
	headCount, errHead := io.ReadFull(file, head)
	if errHead != nil {
		return nil, nil, errHead
	}
	tailSize := int64(maxLogSampleBytes - headCount)
	if _, errSeek := file.Seek(-tailSize, 2); errSeek != nil {
		return nil, nil, errSeek
	}
	tail := make([]byte, int(tailSize))
	tailCount, errTail := io.ReadFull(file, tail)
	if errTail != nil {
		return nil, nil, errTail
	}
	return append(append(head[:headCount], '\n'), tail[:tailCount]...), info, nil
}

func extractRoute(prefix string) string {
	for _, line := range strings.Split(prefix, "\n") {
		if !strings.HasPrefix(line, "URL: ") {
			continue
		}
		rawURL := strings.TrimSpace(strings.TrimPrefix(line, "URL: "))
		parsed, errParse := url.Parse(rawURL)
		if errParse != nil {
			return safeRouteFallback(rawURL)
		}
		parsed.RawQuery = ""
		parsed.Fragment = ""
		parsed.User = nil
		return parsed.String()
	}
	return "unknown"
}

func safeRouteFallback(rawURL string) string {
	withoutQuery := strings.SplitN(rawURL, "?", 2)[0]
	if schemeIndex := strings.Index(withoutQuery, "://"); schemeIndex >= 0 {
		return pathAfterAuthority(withoutQuery[schemeIndex+3:])
	}
	if strings.HasPrefix(withoutQuery, "//") {
		return pathAfterAuthority(strings.TrimPrefix(withoutQuery, "//"))
	}
	if strings.HasPrefix(withoutQuery, "/") {
		return withoutQuery
	}
	return "unknown"
}

func pathAfterAuthority(authorityAndPath string) string {
	if pathIndex := strings.Index(authorityAndPath, "/"); pathIndex >= 0 {
		return authorityAndPath[pathIndex:]
	}
	return "unknown"
}

func extractModel(prefix, route string) string {
	const requestBodyMarker = "=== REQUEST BODY ===\n"
	if bodyIndex := strings.Index(prefix, requestBodyMarker); bodyIndex >= 0 {
		var request requestEnvelope
		body := prefix[bodyIndex+len(requestBodyMarker):]
		if json.NewDecoder(strings.NewReader(body)).Decode(&request) == nil && strings.TrimSpace(request.Model) != "" {
			return strings.TrimSpace(request.Model)
		}
	}
	match := modelPattern.FindStringSubmatch(route)
	if len(match) == 2 {
		return match[1]
	}
	return "unknown"
}

func extractRequestID(filename string) string {
	match := requestIDPattern.FindStringSubmatch(filename)
	if len(match) == 2 {
		return match[1]
	}
	return "unknown"
}

func classifyAPI(route string) string {
	switch {
	case strings.HasPrefix(route, "/v1beta/models/") || strings.Contains(route, ":generateContent"):
		return "Gemini-compatible"
	case strings.HasPrefix(route, "/v1/messages"):
		return "Claude-compatible"
	case strings.HasPrefix(route, "/v1/"):
		return "OpenAI-compatible"
	default:
		return "unknown"
	}
}

func extractErrorMessage(section string, status int) string {
	bodyIndex := strings.Index(section, "\n\n")
	if bodyIndex >= 0 {
		body := strings.TrimSpace(section[bodyIndex+2:])
		var envelope responseEnvelope
		if json.Unmarshal([]byte(body), &envelope) == nil && len(envelope.Error) > 0 {
			var object responseError
			if json.Unmarshal(envelope.Error, &object) == nil && object.Message != "" {
				return object.Message
			}
			var text string
			if json.Unmarshal(envelope.Error, &text) == nil && text != "" {
				return text
			}
		}
	}
	if match := messagePattern.FindStringSubmatch(section); len(match) == 2 {
		var decoded string
		if json.Unmarshal([]byte(`"`+match[1]+`"`), &decoded) == nil {
			return decoded
		}
	}
	return fmt.Sprintf("HTTP %d response", status)
}

func sanitizeMessage(message string) string {
	message = apiKeyPattern.ReplaceAllString(message, "sk-…")
	message = bearerPattern.ReplaceAllString(message, "${1}…")
	message = jsonSecretPattern.ReplaceAllString(message, "${1}…")
	message = googleAPIKeyPattern.ReplaceAllString(message, "AIza…")
	message = jwtPattern.ReplaceAllString(message, "eyJ…")
	message = githubTokenPattern.ReplaceAllString(message, "gh…")
	message = awsAccessKeyPattern.ReplaceAllString(message, "AKIA…")
	message = namedSecretPattern.ReplaceAllString(message, "${1}…")
	message = strings.TrimSpace(message)
	if utf8.RuneCountInString(message) <= 1000 {
		return message
	}
	runes := []rune(message)
	return string(runes[:1000]) + "…"
}

func statusText(status int) string {
	return strconv.Itoa(status)
}
