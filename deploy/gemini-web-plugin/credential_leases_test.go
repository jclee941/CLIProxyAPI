package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestReplaceRejectsStaleExpectedToken_whenLatestItemChanged(t *testing.T) {
	reference, err := parseReference(testMaintenanceSource().TokenRef, "homelab")
	if err != nil {
		t.Fatal(err)
	}
	store := opStore{vault: "homelab", run: func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		if args[1] != "get" {
			t.Fatal("stale credential reached edit")
		}
		return json.Marshal(map[string]interface{}{"category": "SECURE_NOTE", "title": "Preserve", "fields": []map[string]string{{"id": "web-session", "value": encodedToken("external")}}})
	}}

	err = store.ReplaceIfExpected(t.Context(), secretReplacement{Reference: reference, Expected: sessionToken{encodedToken("old")}, Replacement: sessionToken{encodedToken("new")}})

	var public *publicError
	if !errors.As(err, &public) || public.Code != "credential_changed" {
		t.Fatalf("stale compare failed: %v", err)
	}
}

func TestExpectedReplacementPreservesItemMetadata_whenLatestTokenMatches(t *testing.T) {
	reference, err := parseReference(testMaintenanceSource().TokenRef, "homelab")
	if err != nil {
		t.Fatal(err)
	}
	original, replacement := sessionToken{encodedToken("original")}, sessionToken{encodedToken("replacement")}
	edits := 0
	store := opStore{vault: "homelab", run: func(_ context.Context, args []string, input []byte) ([]byte, error) {
		if args[1] == "get" {
			return []byte(`{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaa","version":9,"category":"SECURE_NOTE","title":"Operator title","tags":["operator","retained"],"fields":[{"id":"web-session","label":"web-session","type":"CONCEALED","value":"` + original.value + `"},{"id":"other","label":"Other secret","type":"CONCEALED","value":"synthetic-other"},{"id":"notesPlain","type":"STRING","value":"Operator note"}]}`), nil
		}
		edits++
		if !slices.Equal(args, []string{"item", "edit", reference.item, "--vault", "homelab", "--format", "json"}) || strings.Contains(strings.Join(args, " "), replacement.value) {
			t.Fatal("edit secret escaped JSON stdin")
		}
		var document struct {
			Title   string
			Tags    []string
			Version int
			Fields  []struct{ ID, Value string }
		}
		if err := json.Unmarshal(input, &document); err != nil {
			t.Fatal(err)
		}
		if document.Title != "Operator title" || !slices.Equal(document.Tags, []string{"operator", "retained"}) || document.Version != 9 || len(document.Fields) != 3 {
			t.Fatal("maintenance overwrote item metadata")
		}
		values := make(map[string]string)
		for _, field := range document.Fields {
			values[field.ID] = field.Value
		}
		if values["web-session"] != replacement.value || values["other"] != "synthetic-other" || values["notesPlain"] != "Operator note" {
			t.Fatal("replacement changed unrelated fields")
		}
		return []byte(`{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaa"}`), nil
	}}

	err = store.ReplaceIfExpected(t.Context(), secretReplacement{Reference: reference, Expected: original, Replacement: replacement})

	if err != nil || edits != 1 {
		t.Fatalf("expected replacement failed: %v", err)
	}
}

func TestCredentialLeaseExcludesWriters_whenReaderActive(t *testing.T) {
	service := newService(nil)
	reference := testMaintenanceSource().TokenRef
	reader, err := service.acquireCredential(reference, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.guard.RUnlock()

	_, err = service.acquireCredential(reference, true)

	var public *publicError
	if !errors.As(err, &public) || public.Code != "session_busy" {
		t.Fatalf("writer entered shared lease: %v", err)
	}
}

func TestExpectedReplacementDoesNotAddTags_whenLatestItemHasNone(t *testing.T) {
	reference, err := parseReference(testMaintenanceSource().TokenRef, "homelab")
	if err != nil {
		t.Fatal(err)
	}
	original := sessionToken{encodedToken("original")}
	store := opStore{vault: "homelab", run: func(_ context.Context, args []string, input []byte) ([]byte, error) {
		if args[1] == "get" {
			return []byte(`{"category":"SECURE_NOTE","title":"Operator","fields":[{"id":"web-session","value":"` + original.value + `"}]}`), nil
		}
		var document map[string]json.RawMessage
		if err := json.Unmarshal(input, &document); err != nil {
			t.Fatal(err)
		}
		if _, exists := document["tags"]; exists {
			t.Error("maintenance added tags absent from the latest item")
		}
		return []byte(`{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaa"}`), nil
	}}

	err = store.ReplaceIfExpected(t.Context(), secretReplacement{Reference: reference, Expected: original, Replacement: sessionToken{encodedToken("new")}})

	if err != nil {
		t.Fatal(err)
	}
}
