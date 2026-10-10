package main

import (
	"context"
	"encoding/json"
	"strings"
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

type flowAccountView struct {
	ID         string `json:"id"`
	Label      string `json:"label,omitempty"`
	Status     string `json:"status"`
	Credits    *int   `json:"credits,omitempty"`
	Tier       *int   `json:"tier,omitempty"`
	ObservedAt int64  `json:"observed_at,omitempty"`
	Project    string `json:"project,omitempty"`
	Error      string `json:"error,omitempty"`
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
		record := storageRecord{ID: id, SourceAuthID: id}
		view := flowAccountView{ID: id, Label: labels[id], Status: "unknown"}
		err := service.withFlowSession(reading, record, func(session *flowSession) error {
			payload, err := session.rpc(reading, "nzlxg", []any{}, flowProjectsPath, "")
			if err != nil {
				return err
			}
			credits, ok := flowCredits(payload)
			if !ok {
				return failure(502, "flow_credits_missing")
			}
			view.Credits = &credits
			if tier, valid := jsonInteger(jsonField(payload, 3)); valid && tier > 0 {
				view.Tier = &tier
			}
			view.Status, view.ObservedAt = "ready", service.now().Unix()
			return nil
		})
		if err != nil {
			view.Status, view.Error = "error", safeCredentialMessage(err)
			code := safeCredentialCode(err)
			switch {
			case strings.HasSuffix(code, "busy"):
				view.Status = "busy"
			case strings.Contains(code, "unauthenticated") || strings.Contains(code, "credential_identity_invalid"):
				view.Status = "expired"
			}
		}
		service.flowMu.Lock()
		view.Project = service.flowProjects[id]
		service.flowMu.Unlock()
		accounts = append(accounts, view)
	}
	return map[string]any{"provider": provider, "captcha": service.settings().FlowCaptchaProvider, "models": flowModelInfos(), "accounts": accounts}, nil
}
