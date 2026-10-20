package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"strings"
	"time"
)

// An account's health is ok, blocked (Flow refuses it for unusual activity),
// or failing (its health check failed for another reason). A Telegram card is
// sent when it changes between these, never for a repeat.
const (
	flowHealthOK      = "ok"
	flowHealthBlocked = "blocked"
	flowHealthFailing = "failing"
)

const flowTelegramTestPath = "/v0/management/plugins/flow2api/telegram/test"

// flowTelegramCall bounds one card; a card is a notification, not a model call.
const flowTelegramCall = 10 * time.Second

var flowKST = time.FixedZone("KST", 9*60*60)

type flowHealthState struct {
	status      string
	since       time.Time
	detail      string
	heldUntil   time.Time
	checkedAt   time.Time
	checkKind   string
	checkResult string
	checkError  string
}

type flowHealthView struct {
	Status      string `json:"status"`
	Since       int64  `json:"since,omitempty"`
	Detail      string `json:"detail,omitempty"`
	HeldUntil   int64  `json:"held_until,omitempty"`
	CheckedAt   int64  `json:"checked_at,omitempty"`
	CheckKind   string `json:"check_kind,omitempty"`
	CheckResult string `json:"check_result,omitempty"`
	CheckError  string `json:"check_error,omitempty"`
	AcceptedAt  int64  `json:"accepted_at,omitempty"`
	NextCheck   int64  `json:"next_check,omitempty"`
}

// markHealth records the account's health and sends a card when it changed.
func (service *service) markHealth(account, status, detail string, heldUntil time.Time) {
	now := service.now()
	service.flowMu.Lock()
	if service.healthStates == nil {
		service.healthStates = map[string]flowHealthState{}
	}
	previous := service.healthStates[account]
	current := previous
	current.status, current.detail, current.heldUntil = status, detail, heldUntil
	if previous.status != status {
		current.since = now
	}
	service.healthStates[account] = current
	label := service.accountLabels[account]
	service.flowMu.Unlock()
	if previous.status == status {
		return
	}
	if label == "" {
		label = account
	}
	switch {
	case status == flowHealthBlocked:
		service.sendTelegram(flowBlockedCard(label, heldUntil, now))
	case status == flowHealthFailing:
		service.sendTelegram(flowFailingCard(label, detail, now))
	case previous.status == flowHealthBlocked || previous.status == flowHealthFailing:
		service.sendTelegram(flowRecoveredCard(label, now.Sub(previous.since), now))
	}
}

func (service *service) noteHealthCheck(account, kind string, err error) {
	result, code := "passed", ""
	switch {
	case flowUnusualActivity(err):
		result, code = "refused", "PUBLIC_ERROR_UNUSUAL_ACTIVITY"
	case err != nil:
		result, code = "failed", safeCredentialCode(err)
	}
	service.flowMu.Lock()
	defer service.flowMu.Unlock()
	if service.healthStates == nil {
		service.healthStates = map[string]flowHealthState{}
	}
	state := service.healthStates[account]
	state.checkedAt, state.checkKind, state.checkResult, state.checkError = service.now(), kind, result, code
	service.healthStates[account] = state
}

func (service *service) healthView(account string) *flowHealthView {
	every := service.healthCheckInterval()
	until, held := service.unusualActivityHeld(account)
	service.flowMu.Lock()
	state := service.healthStates[account]
	accepted := service.flowAccepted[account]
	checked := service.healthChecked[account]
	service.flowMu.Unlock()
	view := &flowHealthView{Status: state.status, Detail: state.detail, CheckKind: state.checkKind, CheckResult: state.checkResult, CheckError: state.checkError}
	if view.Status == "" {
		view.Status = "unknown"
	}
	view.Since, view.CheckedAt, view.AcceptedAt = unixOrZero(state.since), unixOrZero(state.checkedAt), unixOrZero(accepted)
	switch {
	case held:
		view.HeldUntil, view.NextCheck = until.Unix(), until.Unix()
	case every > 0:
		last := accepted
		if checked.After(last) {
			last = checked
		}
		next := last.Add(every)
		if now := service.now(); next.Before(now) {
			next = now
		}
		view.NextCheck = next.Unix()
	}
	return view
}

func unixOrZero(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.Unix()
}

func (service *service) rememberLabel(account, label string, replace bool) {
	if label == "" {
		return
	}
	service.flowMu.Lock()
	defer service.flowMu.Unlock()
	if service.accountLabels == nil {
		service.accountLabels = map[string]string{}
	}
	if replace || service.accountLabels[account] == "" {
		service.accountLabels[account] = label
	}
}

func flowBlockedCard(label string, heldUntil, now time.Time) string {
	return strings.Join([]string{
		"<b>🚫 Google Flow 차단 감지</b>",
		"<code>" + html.EscapeString(label) + "</code>",
		"Flow가 이 계정의 생성을 <b>비정상 활동</b>(PUBLIC_ERROR_UNUSUAL_ACTIVITY)으로 거절했습니다.",
		"보류: <b>" + heldUntil.In(flowKST).Format("01-02 15:04") + " KST</b>까지, 그때 헬스체크가 자동으로 다시 확인합니다.",
		"<i>" + now.In(flowKST).Format("2006-01-02 15:04:05") + " KST</i>",
	}, "\n")
}

func flowFailingCard(label, code string, now time.Time) string {
	return strings.Join([]string{
		"<b>⚠️ Google Flow 헬스체크 실패</b>",
		"<code>" + html.EscapeString(label) + "</code>",
		"오류: <code>" + html.EscapeString(code) + "</code>",
		"<i>" + now.In(flowKST).Format("2006-01-02 15:04:05") + " KST</i>",
	}, "\n")
}

func flowRecoveredCard(label string, outage time.Duration, now time.Time) string {
	return strings.Join([]string{
		"<b>✅ Google Flow 복구</b>",
		"<code>" + html.EscapeString(label) + "</code>",
		"생성이 다시 됩니다. 중단 시간: <b>" + outage.Round(time.Minute).String() + "</b>",
		"<i>" + now.In(flowKST).Format("2006-01-02 15:04:05") + " KST</i>",
	}, "\n")
}

// telegramTarget is the configured bot and chat, else the core's.
func (service *service) telegramTarget() (string, string) {
	settings := service.settings()
	token, chat := settings.TelegramBotToken, settings.TelegramChatID
	if token == "" {
		token = strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	}
	if chat == "" {
		chat = strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID"))
	}
	if strings.HasPrefix(token, "op://") || strings.HasPrefix(chat, "op://") {
		return "", ""
	}
	return token, chat
}

// sendTelegram delivers a card in the background; a failure is reported by
// code only, since the bot token is part of the request address.
func (service *service) sendTelegram(card string) {
	go func() {
		if err := service.deliverTelegram(service.lifetime, card); err != nil && safeCredentialCode(err) != "flow_telegram_unconfigured" {
			service.report(map[string]any{"provider": provider, "state": safeCredentialCode(err)}, "flow2api: telegram")
		}
	}()
}

func (service *service) deliverTelegram(ctx context.Context, card string) error {
	token, chat := service.telegramTarget()
	if token == "" || chat == "" {
		return failure(409, "flow_telegram_unconfigured")
	}
	origin := "https://api.telegram.org"
	if service.telegramOrigin != "" {
		origin = service.telegramOrigin
	}
	body, err := json.Marshal(map[string]any{"chat_id": chat, "text": card, "parse_mode": "HTML", "disable_web_page_preview": true})
	if err != nil {
		return failure(500, "flow_telegram_encoding_failed")
	}
	ctx, cancel := context.WithTimeout(ctx, flowTelegramCall)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/bot"+token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return failure(500, "flow_telegram_request_invalid")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := service.client.Do(request)
	if err != nil {
		return failure(502, "flow_telegram_unreachable")
	}
	defer func() {
		if errClose := response.Body.Close(); errClose != nil {
			_ = errClose // The card was answered either way.
		}
	}()
	var reply struct {
		OK bool `json:"ok"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(http.MaxBytesReader(nil, response.Body, 64<<10)).Decode(&reply) != nil || !reply.OK {
		return &publicError{Code: "flow_telegram_rejected", Message: fmt.Sprintf("flow_telegram_rejected: HTTP %d", response.StatusCode), HTTPStatus: 502}
	}
	return nil
}

func (service *service) telegramTest(ctx context.Context) (interface{}, error) {
	card := strings.Join([]string{
		"<b>🔔 Google Flow 알림 테스트</b>",
		"Flow 헬스체크 알림이 이 채팅으로 옵니다: 차단 감지, 헬스체크 실패, 복구.",
		"<i>" + service.now().In(flowKST).Format("2006-01-02 15:04:05") + " KST</i>",
	}, "\n")
	if err := service.deliverTelegram(ctx, card); err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]string{"status": "sent"})
	return httpResponse{StatusCode: 200, Headers: http.Header{"Content-Type": {"application/json"}, "Cache-Control": {"no-store"}}, Body: body}, err
}
