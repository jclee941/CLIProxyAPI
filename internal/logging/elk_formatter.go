package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
)

const (
	logFormatText = "text"
	logFormatJSON = "json"
)

type elkLogEvent struct {
	Timestamp string         `json:"@timestamp"`
	Message   string         `json:"message"`
	Log       elkLogDetails  `json:"log"`
	Service   elkService     `json:"service"`
	Event     elkEvent       `json:"event"`
	HTTP      *elkHTTP       `json:"http,omitempty"`
	Error     *elkError      `json:"error,omitempty"`
	Fields    map[string]any `json:"fields,omitempty"`
}

type elkLogDetails struct {
	Level  string        `json:"level"`
	Origin *elkLogOrigin `json:"origin,omitempty"`
}

type elkLogOrigin struct {
	Function string        `json:"function,omitempty"`
	File     elkOriginFile `json:"file"`
}

type elkOriginFile struct {
	Name string `json:"name"`
	Line int    `json:"line,omitempty"`
}

type elkService struct {
	Name string `json:"name"`
}

type elkEvent struct {
	Dataset string `json:"dataset"`
}

type elkHTTP struct {
	Request elkHTTPRequest `json:"request,omitempty"`
}

type elkHTTPRequest struct {
	ID string `json:"id,omitempty"`
}

type elkError struct {
	Message string `json:"message,omitempty"`
}

type elkFormatter struct{}

func newLogFormatter(format string) (log.Formatter, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", logFormatText:
		return &LogFormatter{}, nil
	case logFormatJSON:
		return &elkFormatter{}, nil
	default:
		return nil, fmt.Errorf("logging: unsupported log format %q (supported: text, json)", format)
	}
}

func (f *elkFormatter) Format(entry *log.Entry) ([]byte, error) {
	event := elkLogEvent{
		Timestamp: entry.Time.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		Message:   strings.TrimRight(entry.Message, "\r\n"),
		Log:       elkLogDetails{Level: normalizedLogLevel(entry.Level)},
		Service:   elkService{Name: "cli-proxy-api"},
		Event:     elkEvent{Dataset: "cliproxy.log"},
		Fields:    elkFields(entry.Data),
	}
	if requestID, ok := entry.Data["request_id"].(string); ok {
		event.HTTP = &elkHTTP{Request: elkHTTPRequest{ID: requestID}}
	}
	if logError, ok := entry.Data[log.ErrorKey]; ok {
		event.Error = &elkError{Message: fmt.Sprint(logError)}
	}
	if entry.Caller != nil {
		event.Log.Origin = &elkLogOrigin{
			Function: entry.Caller.Function,
			File: elkOriginFile{
				Name: filepath.Base(entry.Caller.File),
				Line: entry.Caller.Line,
			},
		}
	}

	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if errEncode := encoder.Encode(event); errEncode != nil {
		return nil, fmt.Errorf("logging: encode JSON event: %w", errEncode)
	}
	return buffer.Bytes(), nil
}

func normalizedLogLevel(level log.Level) string {
	if level == log.WarnLevel {
		return "warn"
	}
	return level.String()
}

func elkFields(data log.Fields) map[string]any {
	fields := make(map[string]any)
	for _, key := range logFieldOrder {
		if key == log.ErrorKey {
			continue
		}
		if value, ok := data[key]; ok {
			fields[key] = value
		}
	}
	if pluginID, ok := data["plugin_id"]; ok && strings.TrimSpace(fmt.Sprint(pluginID)) != "" {
		for _, key := range pluginPathFieldOrder {
			if value, exists := data[key]; exists {
				fields[key] = value
			}
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}
