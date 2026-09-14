package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

type sourceID string
type webAccountID string
type accessCredential struct{ value string }
type hostCaller func(context.Context, string, []byte) ([]byte, error)

type hostAuthEntry struct {
	ID          string `json:"id"`
	AuthIndex   string `json:"auth_index"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	Type        string `json:"type"`
	Source      string `json:"source"`
	Status      string `json:"status"`
	Disabled    bool   `json:"disabled"`
	Unavailable bool   `json:"unavailable"`
	RuntimeOnly bool   `json:"runtime_only"`
}

type codexSourceView struct {
	ID       sourceID `json:"id"`
	Label    string   `json:"label"`
	Disabled bool     `json:"disabled"`
	Provider string   `json:"provider"`
}

type scopedHost struct {
	call       hostCaller
	callbackID string
}

func (plugin *service) bindHost(call hostCaller) {
	plugin.mu.Lock()
	defer plugin.mu.Unlock()
	plugin.host = call
}

func (plugin *service) sourceScope(callbackID string) (scopedHost, *publicError) {
	plugin.mu.RLock()
	defer plugin.mu.RUnlock()
	if !boundedText(callbackID, 256) || plugin.host == nil {
		return scopedHost{}, failure(503, "host_callback_required")
	}
	return scopedHost{plugin.host, callbackID}, nil
}

func (host scopedHost) request(ctx context.Context, method, index string) (json.RawMessage, *publicError) {
	if ctx.Err() != nil {
		return nil, failure(503, "request_cancelled")
	}
	raw, err := json.Marshal(struct {
		CallbackID string `json:"host_callback_id"`
		AuthIndex  string `json:"auth_index,omitempty"`
	}{host.callbackID, index})
	if err != nil {
		return nil, failure(500, "host_request_encoding_failed")
	}
	response, errCall := host.call(ctx, method, raw)
	if errCall != nil {
		return nil, failure(503, "host_auth_unavailable")
	}
	var result struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if len(response) > accountBodyLimit || !utf8.Valid(response) || json.Unmarshal(response, &result) != nil || !result.OK {
		return nil, failure(503, "host_auth_unavailable")
	}
	return result.Result, nil
}

func boundedText(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 32 || character == 127 {
			return false
		}
	}
	return true
}

func parseAccess(value string) (accessCredential, *publicError) {
	if !boundedText(value, 32768) || strings.ContainsAny(value, " \t") {
		return accessCredential{}, failure(409, "source_credential_invalid")
	}
	return accessCredential{value}, nil
}

func opaqueID(prefix, value string) string {
	digest := sha256.Sum256([]byte(prefix + "\x00" + value))
	return prefix + "_" + hex.EncodeToString(digest[:])
}

func entryID(entry hostAuthEntry) sourceID {
	return sourceID(opaqueID("codex", entry.ID+"\x00"+entry.AuthIndex))
}

func physicalCodex(entry hostAuthEntry) bool {
	return entry.Provider == "codex" && entry.Type == "codex" && entry.Source == "file" && !entry.RuntimeOnly && !entry.Unavailable && boundedText(entry.ID, 1024) && boundedText(entry.AuthIndex, 256) && boundedText(entry.Name, 1024)
}

func (host scopedHost) entries(ctx context.Context) ([]hostAuthEntry, *publicError) {
	raw, err := host.request(ctx, "host.auth.list", "")
	if err != nil {
		return nil, err
	}
	var result struct {
		Files []hostAuthEntry `json:"files"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Files == nil {
		return nil, failure(503, "host_auth_unavailable")
	}
	return result.Files, nil
}

func (host scopedHost) runtime(ctx context.Context, entry hostAuthEntry) (hostAuthEntry, *publicError) {
	raw, err := host.request(ctx, "host.auth.get_runtime", entry.AuthIndex)
	if err != nil {
		return hostAuthEntry{}, err
	}
	var result struct {
		Auth hostAuthEntry `json:"auth"`
	}
	if json.Unmarshal(raw, &result) != nil || !physicalCodex(result.Auth) || result.Auth.ID != entry.ID || result.Auth.AuthIndex != entry.AuthIndex || result.Auth.Name != entry.Name {
		return hostAuthEntry{}, failure(409, "source_metadata_unavailable")
	}
	result.Auth.Disabled = result.Auth.Disabled || entry.Disabled || entry.Status == "disabled" || result.Auth.Status == "disabled"
	return result.Auth, nil
}

func (host scopedHost) sources(ctx context.Context) ([]codexSourceView, *publicError) {
	entries, err := host.entries(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]codexSourceView, 0)
	for _, entry := range entries {
		if !physicalCodex(entry) {
			continue
		}
		runtime, errRuntime := host.runtime(ctx, entry)
		if errRuntime != nil {
			return nil, errRuntime
		}
		id := entryID(entry)
		views = append(views, codexSourceView{id, "Codex " + string(id)[6:18], runtime.Disabled, "codex"})
	}
	return views, nil
}

func (host scopedHost) credential(ctx context.Context, id sourceID, allowDisabled bool) (accessCredential, *publicError) {
	entries, err := host.entries(ctx)
	if err != nil {
		return accessCredential{}, err
	}
	var selected *hostAuthEntry
	for _, entry := range entries {
		if entryID(entry) != id {
			continue
		}
		if selected != nil || !physicalCodex(entry) {
			return accessCredential{}, failure(409, "source_metadata_unavailable")
		}
		selected = &entry
	}
	if selected == nil {
		return accessCredential{}, failure(404, "source_not_found")
	}
	runtime, errRuntime := host.runtime(ctx, *selected)
	if errRuntime != nil {
		return accessCredential{}, errRuntime
	}
	if runtime.Disabled && !allowDisabled {
		return accessCredential{}, failure(409, "disabled_source_requires_override")
	}
	raw, errGet := host.request(ctx, "host.auth.get", selected.AuthIndex)
	if errGet != nil {
		return accessCredential{}, errGet
	}
	var record struct {
		AuthIndex string `json:"auth_index"`
		Name      string `json:"name"`
		JSON      struct {
			Type        string  `json:"type"`
			AccessToken string  `json:"access_token"`
			IDToken     *string `json:"id_token"`
			Disabled    bool    `json:"disabled"`
		} `json:"json"`
	}
	if json.Unmarshal(raw, &record) != nil || record.AuthIndex != selected.AuthIndex || record.Name != selected.Name || record.JSON.Type != "codex" {
		return accessCredential{}, failure(409, "source_credential_invalid")
	}
	if record.JSON.Disabled && !allowDisabled {
		return accessCredential{}, failure(409, "disabled_source_requires_override")
	}
	if record.JSON.IDToken != nil && !boundedText(*record.JSON.IDToken, 32768) {
		return accessCredential{}, failure(409, "source_credential_invalid")
	}
	return parseAccess(record.JSON.AccessToken)
}
