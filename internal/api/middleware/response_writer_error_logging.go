package middleware

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	log "github.com/sirupsen/logrus"
)

func (w *ResponseWriterWrapper) logUpstreamRequestError(c *gin.Context, statusCode int, responseErrors []*interfaces.ErrorMessage) {
	if w == nil || statusCode < http.StatusBadRequest {
		return
	}
	fields := log.Fields{"status": strconv.Itoa(statusCode)}
	if w.requestInfo != nil && w.requestInfo.RequestID != "" {
		fields["request_id"] = w.requestInfo.RequestID
	}
	for _, field := range []struct {
		key   string
		label string
	}{
		{key: logging.UpstreamProviderContextKey, label: "provider"},
		{key: logging.UpstreamModelContextKey, label: "model"},
	} {
		if value, exists := c.Get(field.key); exists {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				fields[field.label] = text
			}
		}
	}
	entry := log.WithFields(fields)
	if len(responseErrors) > 0 && responseErrors[0] != nil && responseErrors[0].Error != nil {
		entry = entry.WithError(responseErrors[0].Error)
	} else {
		entry = entry.WithField(log.ErrorKey, responseErrorMessage(w.extractResponseBody(c), statusCode))
	}
	entry.Error("upstream model request failed")
}

func responseErrorMessage(body []byte, statusCode int) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if errUnmarshal := json.Unmarshal(body, &envelope); errUnmarshal == nil {
		if message := strings.TrimSpace(envelope.Error.Message); message != "" {
			return message
		}
		if message := strings.TrimSpace(envelope.Message); message != "" {
			return message
		}
	}
	return http.StatusText(statusCode)
}
