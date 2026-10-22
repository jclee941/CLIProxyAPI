package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

func flowCredits(payload any) (int, bool) {
	values, ok := payload.([]any)
	if !ok || len(values) == 0 {
		return 0, false
	}
	// GetCredits field 1 is the balance. A missing protobuf scalar is zero;
	// subsequent numeric fields are account tiers, not fallback balances.
	if values[0] == nil {
		return 0, true
	}
	value, valid := jsonInteger(values[0])
	return value, valid && value >= 0
}

// flowReading is one GetCredits answer: [credits, paygate tier, plan SKU,
// tier, AI credits, Google Flow credits, [refresh seconds, nanos]]. The Flow
// web client shows field 6 as the Google Flow credits, field 5 as the AI
// credits and field 7 as the time the Google Flow credits refresh.
type flowReading struct {
	credits     int
	paygate     int
	tier        *int
	aiCredits   *int
	flowCredits *int
	refreshAt   time.Time
	observedAt  time.Time
	spends      flowSpendMark
}

// flowSpendMark records the requests that may spend credits as a reading
// begins: how many have begun or ended, and whether none is running.
type flowSpendMark struct {
	sequence uint64
	idle     bool
}

func flowReadingOf(payload any, credits int) flowReading {
	reading := flowReading{credits: credits}
	count := func(index int) *int {
		if value, valid := jsonInteger(jsonField(payload, index)); valid && value >= 0 {
			return &value
		}
		return nil
	}
	reading.paygate, _ = jsonInteger(jsonField(payload, 1))
	if tier := count(3); tier != nil && *tier > 0 {
		reading.tier = tier
	}
	reading.aiCredits, reading.flowCredits = count(4), count(5)
	if seconds, valid := jsonInteger(jsonField(payload, 6, 0)); valid && seconds > 0 {
		nanos, _ := jsonInteger(jsonField(payload, 6, 1))
		reading.refreshAt = time.Unix(int64(seconds), int64(nanos)).UTC()
	}
	return reading
}

// flowBalance reads the credits of the Gemini account record borrows and keeps
// the reading as that account's latest.
func (service *service) flowBalance(ctx context.Context, record storageRecord) (flowReading, error) {
	var reading flowReading
	err := service.withFlowSession(ctx, record, func(session *flowSession) error {
		mark := service.flowSpendSnapshot()
		payload, err := session.rpc(ctx, "nzlxg", []any{}, flowProjectsPath, "")
		if err != nil {
			return err
		}
		credits, ok := flowCredits(payload)
		if !ok {
			return failure(502, "flow_credits_missing")
		}
		reading = service.keepFlowReading(record.SourceAuthID, payload, credits, mark)
		return nil
	})
	return reading, err
}

// keepFlowReading keeps a GetCredits answer as the account's latest balance.
func (service *service) keepFlowReading(account string, payload any, credits int, mark flowSpendMark) flowReading {
	reading := flowReadingOf(payload, credits)
	reading.observedAt, reading.spends = service.now(), mark
	service.flowMu.Lock()
	defer service.flowMu.Unlock()
	if service.flowReadings == nil {
		service.flowReadings = map[string]flowReading{}
	}
	service.flowReadings[account] = reading
	return reading
}

func (service *service) lastFlowReading(account string) (flowReading, bool) {
	service.flowMu.Lock()
	defer service.flowMu.Unlock()
	reading, found := service.flowReadings[account]
	return reading, found
}

// beginFlowSpend runs as a request that may spend credits begins; the function
// it returns runs as the request ends.
func (service *service) beginFlowSpend() func() {
	service.flowMu.Lock()
	service.flowSpending++
	service.flowSpendSequence++
	service.flowMu.Unlock()
	return func() {
		service.flowMu.Lock()
		service.flowSpending--
		service.flowSpendSequence++
		service.flowMu.Unlock()
	}
}

func (service *service) flowSpendSnapshot() flowSpendMark {
	service.flowMu.Lock()
	defer service.flowMu.Unlock()
	return flowSpendMark{sequence: service.flowSpendSequence, idle: service.flowSpending == 0}
}

// flowReadingCurrent reports whether reading is still the balance: no request
// that may spend credits was running when it was taken or has run since.
func (service *service) flowReadingCurrent(reading flowReading) bool {
	return reading.spends.idle && reading.spends.sequence == service.flowSpendSnapshot().sequence
}

type flowAccountView struct {
	ID         string          `json:"id"`
	Label      string          `json:"label,omitempty"`
	Status     string          `json:"status"`
	Credits    *int            `json:"credits,omitempty"`
	Tier       *int            `json:"tier,omitempty"`
	ObservedAt int64           `json:"observed_at,omitempty"`
	Project    string          `json:"project,omitempty"`
	Error      string          `json:"error,omitempty"`
	Health     *flowHealthView `json:"health,omitempty"`
}

func (service *service) flowStatus(ctx context.Context, request managementRequest) (interface{}, error) {
	labels := map[string]string{}
	if service.host != nil {
		if request.HostCallbackID == "" {
			return nil, failure(401, "authenticated_management_callback_required")
		}
		query, err := json.Marshal(struct {
			Callback string `json:"host_callback_id"`
		}{request.HostCallbackID})
		if err != nil {
			return nil, failure(500, "flow_metadata_request_failed")
		}
		raw, err := service.host("host.auth.list", query)
		if err != nil {
			return nil, failure(503, "flow_account_metadata_unavailable")
		}
		var reply envelope
		var listing struct {
			Files []struct {
				ID    string `json:"id"`
				Name  string `json:"name"`
				Label string `json:"label"`
			} `json:"files"`
		}
		if json.Unmarshal(raw, &reply) != nil || !reply.OK || json.Unmarshal(reply.Result, &listing) != nil {
			return nil, failure(502, "flow_account_metadata_invalid")
		}
		for _, entry := range listing.Files {
			label := entry.Label
			if label == "" {
				label = entry.Name
			}
			labels[entry.ID] = label
		}
	}
	accounts := []flowAccountView{}
	reading := withoutLeaseWait(ctx)
	for _, id := range service.settings().FlowAccounts {
		view := flowAccountView{ID: id, Label: labels[id], Status: "unknown"}
		balance, err := service.flowBalance(reading, storageRecord{ID: id, SourceAuthID: id})
		if err == nil {
			view.Credits, view.Tier = &balance.credits, balance.tier
			view.Status, view.ObservedAt = "ready", balance.observedAt.Unix()
		} else {
			view.Status, view.Error = "error", safeCredentialMessage(err)
			code := safeCredentialCode(err)
			switch {
			case strings.HasSuffix(code, "busy"):
				view.Status = "busy"
				// A Gemini turn holds the account; it spends no Flow credits.
				if last, known := service.lastFlowReading(id); known {
					view.Credits, view.Tier, view.ObservedAt = &last.credits, last.tier, last.observedAt.Unix()
				}
			case strings.Contains(code, "unauthenticated") || strings.Contains(code, "credential_identity_invalid"):
				view.Status = "expired"
			}
		}
		service.flowMu.Lock()
		view.Project = service.flowProjects[id]
		service.flowMu.Unlock()
		service.rememberLabel(id, view.Label, true)
		view.Health = service.healthView(id)
		accounts = append(accounts, view)
	}
	token, chat := service.telegramTarget()
	health := map[string]any{"interval_seconds": int64(service.healthCheckInterval() / time.Second), "telegram": token != "" && chat != ""}
	return map[string]any{"provider": provider, "captcha": service.settings().FlowCaptchaProvider, "low_priority_paid_fallback": service.settings().LowPriorityPaidFallback, "models": flowModelInfos(), "accounts": accounts, "health_checks": health}, nil
}
