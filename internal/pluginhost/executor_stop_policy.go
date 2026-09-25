package pluginhost

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type pluginRequestStopError struct{ error }

func (err pluginRequestStopError) Unwrap() error     { return err.error }
func (pluginRequestStopError) IsRequestScoped() bool { return true }

// Schema-6 plugins can supply credential-owned stop rules. Project those onto
// the existing request-scoped error interface: the conductor already suppresses
// refresh, credential rotation, retry and cooldown for this classification.
// This deliberately does not add the upstream configuration-wide action system.
func pluginRequestStopPolicy(auth *coreauth.Auth, err error) error {
	if err == nil || auth == nil {
		return err
	}
	raw, exists := auth.Metadata["request_scoped_errors"]
	if !exists {
		raw, exists = auth.Metadata["request-scoped-errors"]
	}
	if !exists || raw == nil {
		return err
	}
	encoded, encodeErr := json.Marshal(raw)
	if encodeErr != nil {
		return pluginRequestStopError{fmt.Errorf("encode plugin stop policy: %w", errors.Join(err, encodeErr))}
	}
	var rules []struct {
		Status      int      `json:"status"`
		Match       []string `json:"match"`
		MatchRegexr []string `json:"match-regexr"`
		Action      string   `json:"action"`
	}
	if decodeErr := json.Unmarshal(encoded, &rules); decodeErr != nil {
		return pluginRequestStopError{fmt.Errorf("decode plugin stop policy: %w", errors.Join(err, decodeErr))}
	}
	var status interface{ StatusCode() int }
	if !errors.As(err, &status) {
		return err
	}
	for _, rule := range rules {
		if rule.Action != "stop" || rule.Status != status.StatusCode() {
			continue
		}
		for _, match := range rule.Match {
			if match != "" && strings.Contains(err.Error(), match) {
				return pluginRequestStopError{err}
			}
		}
		for _, pattern := range rule.MatchRegexr {
			expression, compileErr := regexp.Compile(pattern)
			if compileErr != nil {
				return pluginRequestStopError{fmt.Errorf("invalid plugin stop policy expression: %w", errors.Join(err, compileErr))}
			}
			if pattern != "" && expression.MatchString(err.Error()) {
				return pluginRequestStopError{err}
			}
		}
	}
	return err
}
