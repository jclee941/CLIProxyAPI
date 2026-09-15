package main

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// A wrong field name here does not fail: the host simply logs nothing, so the
// wire names are pinned rather than left to be noticed in production.
func TestHostLogRequestUsesTheHostFieldNames(t *testing.T) {
	raw, err := json.Marshal(hostLogRequest{
		Level:   "info",
		Message: "m",
		Fields:  map[string]any{"model": "gemini-web-flash-3.8", "outcome": "regenerated"},
	})
	if err != nil {
		t.Fatalf("marshal host log request: %v", err)
	}
	for _, key := range []string{`"level"`, `"message"`, `"fields"`, `"model"`, `"outcome"`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("host log payload missing %s: %s", key, raw)
		}
	}
}

// The host's text formatter renders only a fixed set of field names and drops
// the rest without error, so a report built from prettier names would be written
// as a bare message. These are the names that survive.
func TestReportUsesOnlyFieldNamesTheHostRenders(t *testing.T) {
	rendered := map[string]bool{
		"provider": true, "model": true, "status": true,
		"plugin_id": true, "plugin_name": true, "source_id": true,
		"version": true, "active_version": true, "retired_version": true, "overwritten": true,
		"mode": true, "budget": true, "level": true, "original_mode": true, "original_value": true,
		"min": true, "max": true, "clamped_to": true, "error": true,
		"credential": true, "connection": true, "proxy_scheme": true, "remote_transport": true,
		"media_session_id": true, "call_id": true, "peer": true, "state": true, "reason": true,
	}
	holder := enforcement{model: "gemini-web-flash-3.8", spec: &outputSpec{Kind: "json_schema"}, cfg: defaultConfig()}
	fields := holder.reportFields(0, []string{"celsius must be integer but is string"}, "regenerated")
	if len(fields) == 0 {
		t.Fatal("a repair report carried no fields")
	}
	for key := range fields {
		if !rendered[key] {
			t.Fatalf("field %q is dropped by the host formatter", key)
		}
	}
	for _, required := range []string{"model", "state", "reason"} {
		if _, present := fields[required]; !present {
			t.Fatalf("a repair report must carry %q, got %v", required, fields)
		}
	}
}

func TestReportCountsRegenerationsHonestly(t *testing.T) {
	holder := enforcement{model: "m", spec: &outputSpec{Kind: "json_object"}, cfg: testConfig(2)}
	cases := []struct {
		name    string
		attempt int
		outcome string
		want    string
	}{
		{"first regeneration", 0, outcomeRegenerated, "1 of 2 regenerations"},
		{"second regeneration", 1, outcomeRegenerated, "2 of 2 regenerations"},
		{"giving up", 2, outcomeBudgetExhausted, "2 of 2 regenerations"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			budget, _ := holder.reportFields(testCase.attempt, []string{"x"}, testCase.outcome)["budget"].(string)
			if !strings.HasPrefix(budget, testCase.want) {
				t.Fatalf("budget = %q, want prefix %q", budget, testCase.want)
			}
		})
	}
}

func TestContractKindNamesTheBrokenContract(t *testing.T) {
	tools := parseTools([]byte(`{"messages":[],` + weatherTools + `,"tool_choice":"required"}`))
	cases := []struct {
		name string
		from enforcement
		want string
	}{
		{"demanded tool call", enforcement{tools: tools}, "tool_call"},
		{"schema", enforcement{spec: &outputSpec{Kind: "json_schema"}}, "json_schema"},
		{"json object", enforcement{spec: &outputSpec{Kind: "json_object"}}, "json_object"},
		{"nothing", enforcement{}, "none"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.from.contractKind(); got != testCase.want {
				t.Fatalf("contractKind = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestTruncateRunesNeverSplitsACharacter(t *testing.T) {
	if got := truncateRunes("short", 200); got != "short" {
		t.Fatalf("short text was altered: %q", got)
	}
	long := strings.Repeat("한", 250)
	got := truncateRunes(long, 200)
	if !utf8.ValidString(got) {
		t.Fatalf("truncation produced invalid UTF-8: %q", got)
	}
	if kept := utf8.RuneCountInString(strings.TrimSuffix(got, "...")); kept != 200 {
		t.Fatalf("kept %d runes, want 200", kept)
	}
}
