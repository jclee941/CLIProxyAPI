package main

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestDriveCredentialsReconfigureWithoutDrainingActiveRequests(t *testing.T) {
	// Given: an open store, a request already admitted to its lifecycle, and a
	// cached Drive access token.
	service, _ := loginFixture(t)
	store, epoch := service.localStore(), service.leases.epoch
	if err := service.lifecycle.enter(); err != nil {
		t.Fatal(err)
	}
	defer service.lifecycle.leave()
	service.driveAccess.mu.Lock()
	service.driveAccess.value, service.driveAccess.expires = "cached-access", time.Now().Add(time.Hour)
	service.driveAccess.mu.Unlock()
	config := service.settings()
	config.DriveRefreshToken = "rotated-refresh"
	encoded, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}

	// When: only the Drive refresh token changes through reconfiguration.
	result := invoke(t, service, "plugin.reconfigure", struct {
		ConfigYAML []byte `json:"config_yaml"`
	}{encoded})

	// Then: it applies while the request runs, keeps session state, and drops
	// the cached access token so the next Drive call refreshes with the new one.
	if !result.OK || service.settings().DriveRefreshToken != "rotated-refresh" {
		t.Fatalf("drive rotation failed: %+v", result.Error)
	}
	if service.localStore() != store || service.leases.epoch != epoch {
		t.Fatal("drive rotation replaced session state")
	}
	service.driveAccess.mu.Lock()
	cached := service.driveAccess.value
	service.driveAccess.mu.Unlock()
	if cached != "" {
		t.Fatalf("cached access token survived the rotation: %q", cached)
	}
}
