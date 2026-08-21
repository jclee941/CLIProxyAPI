package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	redactedValue         = "[REDACTED]"
	telegramResponseLimit = 1 << 20
	pollTimeoutMargin     = 10 * time.Second
	allowedUpdatesJSON    = `["message","callback_query"]`
)

// telegramConfig captures the Telegram Bot API endpoint settings.
type telegramConfig struct {
	baseURL     string
	token       string
	pollTimeout time.Duration
	callTimeout time.Duration
}

// telegramClient talks to the Telegram Bot API over HTTP form requests.
type telegramClient struct {
	client *http.Client
	config telegramConfig
}

// telegramResponse is the envelope every Bot API method returns.
type telegramResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

// telegramAPIError reports a non-2xx or ok=false Bot API response.
type telegramAPIError struct {
	Method      string
	Status      int
	Description string
}

func (e *telegramAPIError) Error() string {
	return fmt.Sprintf("Telegram rejected %s with status %d: %s", e.Method, e.Status, e.Description)
}

// telegramChat identifies the conversation an update belongs to.
type telegramChat struct {
	ID int64 `json:"id"`
}

// telegramMessage is the subset of the Message object the bot needs.
type telegramMessage struct {
	MessageID int64        `json:"message_id"`
	Chat      telegramChat `json:"chat"`
	Text      string       `json:"text"`
}

// telegramCallbackQuery carries an inline keyboard button press.
type telegramCallbackQuery struct {
	ID      string           `json:"id"`
	Data    string           `json:"data"`
	Message *telegramMessage `json:"message"`
}

// telegramUpdate is a single long-poll update.
type telegramUpdate struct {
	UpdateID      int64                  `json:"update_id"`
	Message       *telegramMessage       `json:"message"`
	CallbackQuery *telegramCallbackQuery `json:"callback_query"`
}

// inlineKeyboardButton is one inline keyboard cell bound to a callback action.
type inlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// inlineKeyboardMarkup is the reply markup attached to bot messages.
type inlineKeyboardMarkup struct {
	InlineKeyboard [][]inlineKeyboardButton `json:"inline_keyboard"`
}

func newTelegramClient(client *http.Client, config telegramConfig) *telegramClient {
	return &telegramClient{client: client, config: config}
}

// GetUpdates long-polls for new updates starting at the supplied offset.
func (c *telegramClient) GetUpdates(ctx context.Context, offset int64) ([]telegramUpdate, error) {
	pollSeconds := int(c.config.pollTimeout / time.Second)
	form := url.Values{
		"timeout":         {strconv.Itoa(pollSeconds)},
		"allowed_updates": {allowedUpdatesJSON},
	}
	if offset > 0 {
		form.Set("offset", strconv.FormatInt(offset, 10))
	}
	result, errCall := c.call(ctx, "getUpdates", form, c.config.pollTimeout+pollTimeoutMargin)
	if errCall != nil {
		return nil, errCall
	}
	var updates []telegramUpdate
	if len(result) == 0 {
		return updates, nil
	}
	if errDecode := json.Unmarshal(result, &updates); errDecode != nil {
		return nil, fmt.Errorf("decode Telegram updates: %w", errDecode)
	}
	return updates, nil
}

// SendMessage posts a new HTML message with an optional inline keyboard.
func (c *telegramClient) SendMessage(ctx context.Context, chatID int64, text string, keyboard *inlineKeyboardMarkup) error {
	form := url.Values{
		"chat_id":                  {strconv.FormatInt(chatID, 10)},
		"text":                     {text},
		"parse_mode":               {"HTML"},
		"disable_web_page_preview": {"true"},
	}
	if errMarkup := attachKeyboard(form, keyboard); errMarkup != nil {
		return errMarkup
	}
	_, errCall := c.call(ctx, "sendMessage", form, c.config.callTimeout)
	return errCall
}

// EditMessageText replaces the text and keyboard of an existing message.
func (c *telegramClient) EditMessageText(ctx context.Context, chatID, messageID int64, text string, keyboard *inlineKeyboardMarkup) error {
	form := url.Values{
		"chat_id":                  {strconv.FormatInt(chatID, 10)},
		"message_id":               {strconv.FormatInt(messageID, 10)},
		"text":                     {text},
		"parse_mode":               {"HTML"},
		"disable_web_page_preview": {"true"},
	}
	if errMarkup := attachKeyboard(form, keyboard); errMarkup != nil {
		return errMarkup
	}
	_, errCall := c.call(ctx, "editMessageText", form, c.config.callTimeout)
	return errCall
}

// AnswerCallbackQuery acknowledges a button press so the client stops spinning.
func (c *telegramClient) AnswerCallbackQuery(ctx context.Context, callbackID string) error {
	form := url.Values{"callback_query_id": {callbackID}}
	_, errCall := c.call(ctx, "answerCallbackQuery", form, c.config.callTimeout)
	return errCall
}

func attachKeyboard(form url.Values, keyboard *inlineKeyboardMarkup) error {
	if keyboard == nil {
		return nil
	}
	encoded, errEncode := json.Marshal(keyboard)
	if errEncode != nil {
		return fmt.Errorf("encode Telegram reply markup: %w", errEncode)
	}
	form.Set("reply_markup", string(encoded))
	return nil
}

func (c *telegramClient) call(ctx context.Context, method string, form url.Values, timeout time.Duration) (json.RawMessage, error) {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	endpoint := strings.TrimRight(c.config.baseURL, "/") + "/bot" + c.config.token + "/" + method
	request, errRequest := http.NewRequestWithContext(callCtx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if errRequest != nil {
		return nil, fmt.Errorf("create Telegram %s request: %s", method, c.redact(errRequest.Error()))
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, errDo := c.client.Do(request)
	if errDo != nil {
		return nil, fmt.Errorf("send Telegram %s request: %s", method, c.redact(errDo.Error()))
	}
	defer func() {
		if errClose := response.Body.Close(); errClose != nil {
			log.WithError(errClose).Warn("failed to close Telegram response")
		}
	}()

	body, errRead := io.ReadAll(io.LimitReader(response.Body, telegramResponseLimit))
	if errRead != nil {
		return nil, fmt.Errorf("read Telegram %s response: %s", method, c.redact(errRead.Error()))
	}
	var parsed telegramResponse
	if errDecode := json.Unmarshal(body, &parsed); errDecode != nil {
		return nil, fmt.Errorf("decode Telegram %s response: %w", method, errDecode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !parsed.OK {
		return nil, &telegramAPIError{Method: method, Status: response.StatusCode, Description: parsed.Description}
	}
	return parsed.Result, nil
}

func (c *telegramClient) redact(message string) string {
	if c.config.token == "" {
		return message
	}
	return strings.ReplaceAll(message, c.config.token, redactedValue)
}
