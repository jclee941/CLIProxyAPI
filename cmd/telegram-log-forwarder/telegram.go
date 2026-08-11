package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	log "github.com/sirupsen/logrus"
)

type telegramConfig struct {
	baseURL string
	token   string
	chatID  string
}

type telegramSender struct {
	client *http.Client
	config telegramConfig
}

type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

type telegramAPIError struct {
	Status      int
	Description string
}

func (e *telegramAPIError) Error() string {
	return fmt.Sprintf("Telegram rejected message with status %d: %s", e.Status, e.Description)
}

func (e *telegramAPIError) isPermanentMessageFailure() bool {
	if e.Status != http.StatusBadRequest {
		return false
	}
	description := strings.ToLower(e.Description)
	return strings.Contains(description, "message is too long") ||
		strings.Contains(description, "can't parse entities") ||
		strings.Contains(description, "message text is empty")
}

func newTelegramSender(client *http.Client, config telegramConfig) *telegramSender {
	return &telegramSender{client: client, config: config}
}

func (s *telegramSender) Send(ctx context.Context, alert errorAlert) error {
	form := url.Values{
		"chat_id":                  {s.config.chatID},
		"text":                     {renderTelegramMessage(alert)},
		"parse_mode":               {"HTML"},
		"disable_web_page_preview": {"true"},
	}
	endpoint := strings.TrimRight(s.config.baseURL, "/") + "/bot" + s.config.token + "/sendMessage"
	request, errRequest := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if errRequest != nil {
		safeError := strings.ReplaceAll(errRequest.Error(), s.config.token, "[REDACTED]")
		return fmt.Errorf("create Telegram request: %s", safeError)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, errDo := s.client.Do(request)
	if errDo != nil {
		safeError := strings.ReplaceAll(errDo.Error(), s.config.token, "[REDACTED]")
		return fmt.Errorf("send Telegram request: %s", safeError)
	}
	defer func() {
		if errClose := response.Body.Close(); errClose != nil {
			log.WithError(errClose).Warn("failed to close Telegram response")
		}
	}()

	body, errRead := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if errRead != nil {
		return fmt.Errorf("read Telegram response: %w", errRead)
	}
	var result telegramResponse
	if errDecode := json.Unmarshal(body, &result); errDecode != nil {
		return fmt.Errorf("decode Telegram response: %w", errDecode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !result.OK {
		return &telegramAPIError{Status: response.StatusCode, Description: result.Description}
	}
	return nil
}
