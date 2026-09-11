package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
)

type secretWrite struct {
	Label    string
	Token    sessionToken
	Existing secretReference
}
type secretStore interface {
	Resolve(context.Context, secretReference) (sessionToken, error)
	Put(context.Context, secretWrite) (secretReference, error)
}
type commandRunner func(context.Context, []string, []byte) ([]byte, error)
type opStore struct {
	vault string
	run   commandRunner
}

func runOP(ctx context.Context, args []string, input []byte) ([]byte, error) {
	command := exec.CommandContext(ctx, "op", args...)
	command.Stdin = bytes.NewReader(input)
	command.Stderr = io.Discard
	var output bytes.Buffer
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return nil, failure(503, "secret_store_unavailable")
	}
	return output.Bytes(), nil
}

func (store opStore) Resolve(ctx context.Context, reference secretReference) (sessionToken, error) {
	checked, err := parseReference(reference.value, store.vault)
	if err != nil {
		return sessionToken{}, err
	}
	raw, err := store.run(ctx, []string{"read", checked.value, "--no-newline"}, nil)
	if err != nil {
		return sessionToken{}, failure(503, "secret_store_unavailable")
	}
	return parseToken(string(raw))
}

func (store opStore) Put(ctx context.Context, request secretWrite) (secretReference, error) {
	field, err := json.Marshal(struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Type  string `json:"type"`
		Value string `json:"value"`
	}{"web-session", "web-session", "CONCEALED", request.Token.value})
	if err != nil {
		return secretReference{}, failure(500, "secret_encoding_failed")
	}
	document := map[string]json.RawMessage{"category": json.RawMessage(`"SECURE_NOTE"`), "tags": json.RawMessage(`["gemini-web"]`)}
	fields := []json.RawMessage{field}
	args := []string{"item", "create", "--vault", store.vault, "--format", "json"}
	if request.Existing.value != "" {
		reference, err := parseReference(request.Existing.value, store.vault)
		if err != nil {
			return secretReference{}, err
		}
		raw, err := store.run(ctx, []string{"item", "get", reference.item, "--vault", store.vault, "--format", "json"}, nil)
		if err != nil || json.Unmarshal(raw, &document) != nil {
			return secretReference{}, failure(503, "secret_store_unavailable")
		}
		var category string
		if json.Unmarshal(document["category"], &category) != nil || category != "SECURE_NOTE" {
			return secretReference{}, failure(400, "secret_item_category_not_supported")
		}
		var previous []json.RawMessage
		if json.Unmarshal(document["fields"], &previous) != nil {
			return secretReference{}, failure(502, "secret_item_invalid")
		}
		for _, rawField := range previous {
			var existing struct {
				ID, Label string
				Section   json.RawMessage
			}
			if json.Unmarshal(rawField, &existing) != nil {
				return secretReference{}, failure(502, "secret_item_invalid")
			}
			if existing.ID == "web-session" || existing.Label == "web-session" {
				if len(existing.Section) > 0 && string(existing.Section) != "null" {
					return secretReference{}, failure(400, "secret_item_field_ambiguous")
				}
				continue
			}
			fields = append(fields, rawField)
		}
		args = []string{"item", "edit", reference.item, "--vault", store.vault, "--format", "json"}
	}
	document["title"], err = json.Marshal(request.Label)
	if err != nil {
		return secretReference{}, failure(500, "secret_encoding_failed")
	}
	document["fields"], err = json.Marshal(fields)
	if err != nil {
		return secretReference{}, failure(500, "secret_encoding_failed")
	}
	input, err := json.Marshal(document)
	if err != nil {
		return secretReference{}, failure(500, "secret_encoding_failed")
	}
	output, err := store.run(ctx, args, input)
	if err != nil {
		return secretReference{}, failure(503, "secret_store_unavailable")
	}
	var result struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(output, &result) != nil {
		return secretReference{}, failure(502, "secret_item_invalid")
	}
	return parseReference("op://"+store.vault+"/"+result.ID+"/web-session", store.vault)
}
