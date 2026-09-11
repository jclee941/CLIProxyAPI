package main

import (
	"context"
	"regexp"
	"time"
)

const (
	credentialWorkerBudget  = time.Minute
	credentialFenceDuration = 70 * time.Second
	maintenanceAuthCooldown = 30 * time.Minute
	profileCDPOrigin        = "http://192.168.50.220:9222"
	maintainPath            = "/plugins/gemini-web/maintain"
)

var (
	accountDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	profileGUIDPattern   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

type maintenanceSource struct {
	TokenRef           string  `json:"token_ref" yaml:"token_ref"`
	ProfileGUID        string  `json:"profile_guid" yaml:"profile_guid"`
	ExpectedGaiaSHA256 string  `json:"expected_gaia_sha256" yaml:"expected_gaia_sha256"`
	AuthUser           *uint64 `json:"auth_user,omitempty" yaml:"auth_user,omitempty"`
}

type captureRequest struct {
	Binding  maintenanceSource
	Bindings map[string]maintenanceSource
	AuthUser uint64
}

type capturedSession struct {
	Token         sessionToken
	AccountSHA256 string
}

type credentialSource interface {
	Capture(context.Context, captureRequest) (capturedSession, error)
}

type credentialInspection struct {
	AccountSHA256 string `json:"account_sha256"`
	AuthUser      uint64 `json:"auth_user"`
}

func validateMaintenanceSources(sources map[string]maintenanceSource, vault string) error {
	references := make(map[string]bool, len(sources))
	identities := make(map[string]bool, len(sources))
	profiles := make(map[string]bool, len(sources))
	for id, source := range sources {
		if !accountIDPattern.MatchString(id) || !accountDigestPattern.MatchString(source.ExpectedGaiaSHA256) || !profileGUIDPattern.MatchString(source.ProfileGUID) {
			return failure(400, "invalid_maintenance_sources")
		}
		if _, err := parseReference(source.TokenRef, vault); err != nil {
			return failure(400, "invalid_maintenance_sources")
		}
		if references[source.TokenRef] || identities[source.ExpectedGaiaSHA256] || profiles[source.ProfileGUID] {
			return failure(400, "duplicate_maintenance_sources")
		}
		references[source.TokenRef] = true
		identities[source.ExpectedGaiaSHA256] = true
		profiles[source.ProfileGUID] = true
	}
	return nil
}
