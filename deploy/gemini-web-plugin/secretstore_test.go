package main

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestOPCreatePassesConcealedTokenOnlyViaJSONStdin(t *testing.T) {
	token := sessionToken{encodedToken("test-stdin")}
	store := opStore{vault: "homelab", run: func(_ context.Context, args []string, input []byte) ([]byte, error) {
		if !slices.Equal(args, []string{"item", "create", "--vault", "homelab", "--format", "json"}) {
			t.Fatalf("unexpected JSON-stdin create argv: %q", args)
		}
		if strings.Contains(strings.Join(args, " "), token.value) || strings.Contains(strings.Join(args, " "), "Test Label") {
			t.Fatal("sensitive/label data in argv")
		}
		var document struct {
			Category string
			Title    string
			Fields   []struct{ Label, Type, Value string }
		}
		if err := json.Unmarshal(input, &document); err != nil {
			t.Fatal(err)
		}
		if document.Category != "SECURE_NOTE" || document.Title != "<Test Label>" || len(document.Fields) != 1 || document.Fields[0].Type != "CONCEALED" || document.Fields[0].Value != token.value {
			t.Fatal("invalid concealed item template")
		}
		return []byte(`{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaa"}`), nil
	}}
	reference, err := store.Put(t.Context(), secretWrite{Label: "<Test Label>", Token: token})
	if err != nil || reference.value != "op://homelab/aaaaaaaaaaaaaaaaaaaaaaaaaa/web-session" {
		t.Fatalf("invalid reference: %v", err)
	}
}

func TestOPCreateAcceptsJSONStdin_whenRealCLIDryRun(t *testing.T) {
	if os.Getenv("GEMINI_WEB_OP_DRY_RUN_TEST") != "1" {
		t.Skip("set GEMINI_WEB_OP_DRY_RUN_TEST=1 to run the installed op CLI with synthetic input and --dry-run only")
	}
	token := sessionToken{encodedToken("synthetic-cli-dry-run")}
	var preview struct {
		ID, Title, Category string
		Fields              []struct{ ID, Label, Type, Value string }
	}
	store := opStore{vault: "homelab", run: func(ctx context.Context, args []string, input []byte) ([]byte, error) {
		if len(args) < 2 || args[0] != "item" || args[1] != "create" {
			t.Fatal("dry-run test permits only item create")
		}
		output, err := runOP(ctx, append(args, "--dry-run"), input)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(output, &preview); err != nil {
			t.Fatal("CLI did not return a JSON preview")
		}
		return output, nil
	}}

	reference, err := store.Put(t.Context(), secretWrite{Label: "Synthetic CLI contract dry-run", Token: token})

	if err != nil {
		t.Fatalf("real CLI rejected JSON stdin: %v", err)
	}
	if preview.Title != "Synthetic CLI contract dry-run" || preview.Category != "SECURE_NOTE" || reference.item != preview.ID {
		t.Fatal("CLI did not consume the item template")
	}
	for _, field := range preview.Fields {
		if field.ID == "web-session" && field.Label == "web-session" && field.Type == "CONCEALED" && field.Value == token.value {
			return
		}
	}
	t.Fatal("CLI did not preserve the concealed synthetic field")
}

func TestOPEditPreservesUnrelatedFields_andUsesJSONStdin(t *testing.T) {
	calls := 0
	store := opStore{vault: "homelab", run: func(_ context.Context, args []string, input []byte) ([]byte, error) {
		calls++
		if args[1] == "get" {
			return []byte(`{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaa","category":"SECURE_NOTE","title":"Old","fields":[{"id":"notesPlain","type":"STRING","value":"existing note"},{"id":"other","type":"CONCEALED","value":"test-preserved"},{"id":"web-session","label":"web-session","type":"CONCEALED","value":"old-test-value"}]}`), nil
		}
		if args[1] != "edit" || strings.Contains(strings.Join(args, " "), "test-update") {
			t.Fatal("unsafe edit invocation")
		}
		var document struct{ Fields []struct{ ID, Value string } }
		if err := json.Unmarshal(input, &document); err != nil {
			t.Fatal(err)
		}
		if len(document.Fields) != 3 || document.Fields[2].Value != "test-preserved" || strings.Contains(string(input), "old-test-value") {
			t.Fatal("edit replaced unrelated fields or kept stale token")
		}
		return []byte(`{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaa"}`), nil
	}}
	reference, err := parseReference("op://homelab/aaaaaaaaaaaaaaaaaaaaaaaaaa/web-session", "homelab")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Put(t.Context(), secretWrite{Label: "Updated", Token: sessionToken{encodedToken("test-update")}, Existing: reference})
	if err != nil || calls != 2 {
		t.Fatalf("edit failed: %v", err)
	}
}

func TestReferenceRejectsOtherVaultsFieldsAndItemNames(t *testing.T) {
	for _, reference := range []string{"op://private/aaaaaaaaaaaaaaaaaaaaaaaaaa/web-session", "op://homelab/aaaaaaaaaaaaaaaaaaaaaaaaaa/password", "op://homelab/item-name/web-session", "op://homelab/aaaaaaaaaaaaaaaaaaaaaaaaaa/web-session?x=y", "op://homelab/aaaaaaaaaaaaaaaaaaaaaaaaaa/section/web-session"} {
		if _, err := parseReference(reference, "homelab"); err == nil {
			t.Fatal("arbitrary secret reference accepted")
		}
	}
}
