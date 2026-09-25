package logging

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	log "github.com/sirupsen/logrus"
)

func TestNewLogFormatterReturnsELKJSONWhenJSONConfigured(t *testing.T) {
	// Given
	formatter, errFormatter := newLogFormatter("json")
	if errFormatter != nil {
		t.Fatalf("newLogFormatter() error = %v", errFormatter)
	}
	entry := log.NewEntry(log.New())
	entry.Time = time.Date(2026, 8, 11, 10, 20, 30, 123456789, time.UTC)
	entry.Level = log.ErrorLevel
	entry.Message = "upstream request failed"
	entry.Data["request_id"] = "req-123"
	entry.Data["provider"] = "codex"
	entry.Data["error"] = errors.New("connection reset")
	entry.Data["authorization"] = "Bearer secret"

	// When
	formatted, errFormat := formatter.Format(entry)

	// Then
	if errFormat != nil {
		t.Fatalf("Format() error = %v", errFormat)
	}
	var event elkLogEvent
	if errUnmarshal := json.Unmarshal(formatted, &event); errUnmarshal != nil {
		t.Fatalf("Format() output is not JSON: %v", errUnmarshal)
	}
	if event.Timestamp != "2026-08-11T10:20:30.123456789Z" {
		t.Fatalf("@timestamp = %q", event.Timestamp)
	}
	if event.Message != "upstream request failed" || event.Log.Level != "error" {
		t.Fatalf("message/log.level = %q/%q", event.Message, event.Log.Level)
	}
	if event.Service.Name != "cli-proxy-api" || event.Event.Dataset != "cliproxy.log" {
		t.Fatalf("service.name/event.dataset = %q/%q", event.Service.Name, event.Event.Dataset)
	}
	if event.HTTP == nil || event.HTTP.Request.ID != "req-123" {
		t.Fatalf("http = %#v", event.HTTP)
	}
	if event.Fields["provider"] != "codex" {
		t.Fatalf("fields.provider = %#v", event.Fields["provider"])
	}
	if event.Error == nil || event.Error.Message != "connection reset" {
		t.Fatalf("error = %#v", event.Error)
	}
	if _, exposed := event.Fields["authorization"]; exposed {
		t.Fatal("authorization field was exposed")
	}
}

func TestNewLogFormatterRejectsUnknownFormat(t *testing.T) {
	// When
	_, errFormatter := newLogFormatter("xml")

	// Then
	if errFormatter == nil {
		t.Fatal("newLogFormatter() error = nil")
	}
}

func TestConfigureLogOutputUsesJSONFormatFromConfig(t *testing.T) {
	// Given
	t.Cleanup(func() {
		_ = ConfigureLogOutput(&config.Config{LogFormat: logFormatText})
	})

	// When
	errConfigure := ConfigureLogOutput(&config.Config{LogFormat: logFormatJSON})

	// Then
	if errConfigure != nil {
		t.Fatalf("ConfigureLogOutput() error = %v", errConfigure)
	}
	if _, ok := log.StandardLogger().Formatter.(*elkFormatter); !ok {
		t.Fatalf("formatter = %T", log.StandardLogger().Formatter)
	}
}
