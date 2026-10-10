package main

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
)

type sessionExchangeRequestID string

var sessionExchangeRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// An omitted ID preserves ordinary exchanges; an explicitly supplied ID must
// be a nonempty ASCII token, including when the JSON value is null.
func (id *sessionExchangeRequestID) UnmarshalJSON(raw []byte) error {
	var value string
	if json.Unmarshal(raw, &value) != nil || !sessionExchangeRequestIDPattern.MatchString(value) {
		return sessionExchangeError(400, "request_invalid")
	}
	*id = sessionExchangeRequestID(value)
	return nil
}

type sessionExchangeKey struct {
	AuthID    string                   `json:"auth_id"`
	RequestID sessionExchangeRequestID `json:"request_id"`
}

func (service *service) beginSessionExchange(ctx context.Context, key sessionExchangeKey) (context.Context, func(), error) {
	owned, cancel := context.WithCancel(ctx)
	if key.RequestID != "" {
		service.sessionExchangesMu.Lock()
		if service.sessionExchanges[key] != nil {
			service.sessionExchangesMu.Unlock()
			cancel()
			return nil, nil, sessionExchangeError(409, "request_active")
		}
		if service.sessionExchanges == nil {
			service.sessionExchanges = make(map[sessionExchangeKey]context.CancelFunc)
		}
		service.sessionExchanges[key] = cancel
		service.sessionExchangesMu.Unlock()
	}
	// Shutdown closes this signal before draining the handles that hold the
	// credential leases. This also cancels exchanges without a caller ID.
	closing := service.lifecycle.closingSignal()
	go func() {
		select {
		case <-closing:
			cancel()
		case <-owned.Done():
		}
	}()
	return owned, func() {
		cancel()
		if key.RequestID != "" {
			service.sessionExchangesMu.Lock()
			delete(service.sessionExchanges, key)
			service.sessionExchangesMu.Unlock()
		}
	}, nil
}

func (service *service) cancelSessionExchange(request managementRequest) (interface{}, error) {
	var key sessionExchangeKey
	if strictJSON(request.Body, &key) != nil || key.AuthID == "" || key.RequestID == "" {
		return nil, sessionExchangeError(400, "request_invalid")
	}
	if !slices.Contains(service.settings().SessionExchangeAccounts, key.AuthID) {
		return nil, sessionExchangeError(403, "account_denied")
	}
	// Authenticate the account through the same host callback as an exchange,
	// without acquiring its credential lease or inspecting its active turn.
	// Disabling an account does not prevent cancelling work already in flight.
	if _, _, err := service.findRecord(request.HostCallbackID, key.AuthID); err != nil {
		if safeCredentialCode(err) == "account_not_found" {
			return nil, sessionExchangeError(404, "account_not_found")
		}
		return nil, sessionExchangeFailure(err)
	}
	service.sessionExchangesMu.Lock()
	cancel := service.sessionExchanges[key]
	service.sessionExchangesMu.Unlock()
	if cancel != nil {
		cancel()
	}
	// Presence means only that cancellation was requested for an active local
	// operation, not that Google confirmed cancellation or rolled anything back.
	return struct {
		Requested bool `json:"requested"`
		Active    bool `json:"active"`
	}{true, cancel != nil}, nil
}
