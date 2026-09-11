package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

type sessionToken struct{ value string }
type secretReference struct{ item, value string }

var itemIDPattern = regexp.MustCompile(`^[a-z0-9]{26}$`)
var accountIDPattern = regexp.MustCompile(`^gemini-web-[a-z0-9-]+\.json$`)

func parseToken(raw string) (sessionToken, error) {
	const prefix = "gemini-web:v1:"
	if len(raw) > 32768 || !strings.HasPrefix(raw, prefix) {
		return sessionToken{}, failure(400, "invalid_session_token")
	}
	encoded := strings.TrimPrefix(raw, prefix)
	payload, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || base64.RawURLEncoding.EncodeToString(payload) != encoded || !utf8.Valid(payload) {
		return sessionToken{}, failure(400, "invalid_session_token")
	}
	var session struct {
		Cookie   string  `json:"cookie"`
		AuthUser *uint64 `json:"auth_user"`
	}
	if err := strictJSON(payload, &session); err != nil || session.AuthUser == nil || strings.TrimSpace(session.Cookie) == "" {
		return sessionToken{}, failure(400, "invalid_session_token")
	}
	for _, character := range session.Cookie {
		if character < 32 || character > 126 {
			return sessionToken{}, failure(400, "invalid_session_token")
		}
	}
	return sessionToken{value: raw}, nil
}

func parseReference(raw, vault string) (secretReference, error) {
	prefix := "op://" + vault + "/"
	if !strings.HasPrefix(raw, prefix) || !strings.HasSuffix(raw, "/web-session") {
		return secretReference{}, failure(400, "invalid_token_reference")
	}
	item := strings.TrimSuffix(strings.TrimPrefix(raw, prefix), "/web-session")
	if !itemIDPattern.MatchString(item) {
		return secretReference{}, failure(400, "invalid_token_reference")
	}
	return secretReference{item: item, value: raw}, nil
}

func strictJSON(raw []byte, target interface{}) error {
	if !json.Valid(raw) {
		return failure(400, "invalid_json")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := uniqueJSON(decoder); err != nil {
		return err
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return failure(400, "invalid_json_schema")
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF {
		return failure(400, "invalid_json")
	}
	return nil
}

func uniqueJSON(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return failure(400, "invalid_json")
	}
	if token == nil {
		return failure(400, "invalid_json_schema")
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return failure(400, "invalid_json")
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return failure(400, "duplicate_json_field")
			}
			seen[key] = true
			if err := uniqueJSON(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := uniqueJSON(decoder); err != nil {
				return err
			}
		}
	default:
		return failure(400, "invalid_json")
	}
	if _, err := decoder.Token(); err != nil {
		return failure(400, "invalid_json")
	}
	return nil
}

func (service *service) parseStorage(raw []byte, strict bool) (storageRecord, error) {
	var stored struct {
		storageRecord
		RequestScopedErrors []stopRule `json:"request_scoped_errors,omitempty"`
	}
	var err error
	if strict {
		err = strictJSON(raw, &stored)
	} else {
		err = json.Unmarshal(raw, &stored)
	}
	record := stored.storageRecord
	if err != nil || record.Type != provider || !accountIDPattern.MatchString(record.ID) || strings.TrimSpace(record.Label) == "" {
		return record, failure(400, "invalid_auth_storage")
	}
	if _, err := parseReference(record.TokenRef, service.settings().Vault); err != nil {
		return record, err
	}
	return record, nil
}

func authFromRecord(record storageRecord) (authData, error) {
	rules := make([]stopRule, 0, 300)
	for status := 300; status < 600; status++ {
		rules = append(rules, stopRule{Status: status, Match: []string{"gemini_web_omni:"}, Action: "stop"})
	}
	raw, err := json.Marshal(struct {
		storageRecord
		RequestScopedErrors []stopRule `json:"request_scoped_errors"`
	}{record, rules})
	if err != nil {
		return authData{}, failure(500, "auth_encoding_failed")
	}
	return authData{Provider: provider, ID: record.ID, FileName: record.ID, Label: record.Label, ProxyURL: "direct", Disabled: record.Disabled, StorageJSON: raw, Metadata: authMetadata{Type: provider, TokenRef: record.TokenRef, RequestScopedErrors: rules, SessionRevision: record.SessionRevision}}, nil
}
