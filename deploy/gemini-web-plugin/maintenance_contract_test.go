package main

import (
	"strings"
	"testing"
	"time"
)

func testMaintenanceSource() maintenanceSource {
	return maintenanceSource{
		TokenRef:           "op://homelab/aaaaaaaaaaaaaaaaaaaaaaaaaa/web-session",
		ProfileGUID:        "11111111-1111-1111-1111-111111111111",
		ExpectedGaiaSHA256: strings.Repeat("a", 64),
	}
}

func TestMaintenanceSourcesRequireDistinctBoundIdentities(t *testing.T) {
	for _, change := range []struct {
		name string
		edit func(*maintenanceSource)
	}{
		{"reference", func(source *maintenanceSource) { source.TokenRef = "op://other/aaaaaaaaaaaaaaaaaaaaaaaaaa/web-session" }},
		{"fingerprint", func(source *maintenanceSource) { source.ExpectedGaiaSHA256 = "unknown" }},
		{"guid", func(source *maintenanceSource) { source.ProfileGUID = "tab-id" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			source := testMaintenanceSource()
			change.edit(&source)
			if validateMaintenanceSources(map[string]maintenanceSource{"gemini-web-one.json": source}, "homelab") == nil {
				t.Fatal("invalid maintenance source accepted")
			}
		})
	}
	source := testMaintenanceSource()
	if err := validateMaintenanceSources(map[string]maintenanceSource{"gemini-web-one.json": source}, "homelab"); err != nil {
		t.Fatal(err)
	}
	if validateMaintenanceSources(map[string]maintenanceSource{"gemini-web-one.json": source, "gemini-web-two.json": source}, "homelab") == nil {
		t.Fatal("two registrations must not share a secret or account binding")
	}
	second := source
	second.TokenRef = "op://homelab/bbbbbbbbbbbbbbbbbbbbbbbbbb/web-session"
	second.ProfileGUID = "22222222-2222-2222-2222-222222222222"
	if validateMaintenanceSources(map[string]maintenanceSource{"gemini-web-one.json": source, "gemini-web-two.json": second}, "homelab") == nil {
		t.Fatal("the same Google identity must not be rebound under a second reference")
	}
}

func TestMaintenanceCredentialBudgetsAreNotGenerationTimeouts(t *testing.T) {
	if credentialWorkerBudget != time.Minute || credentialFenceDuration <= credentialWorkerBudget {
		t.Fatal("credential fence must outlast the bounded credential worker")
	}
	if maintenanceAuthCooldown != 30*time.Minute {
		t.Fatal("authentication failure cooldown contract changed")
	}
}
