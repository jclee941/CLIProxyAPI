package main

import (
	"testing"
	"time"
)

func TestDriveCredentialChangeInvalidatesCachedAuthorization(t *testing.T) {
	service := newService(nil)
	service.config.DriveRefreshToken = "previous-refresh"
	service.driveAccess.value = "previous-access"
	service.driveAccess.scopes = "https://www.googleapis.com/auth/drive.readonly"
	service.driveAccess.expires = time.Now().Add(time.Hour)
	result := invoke(t, service, "plugin.reconfigure", struct {
		ConfigYAML []byte `json:"config_yaml"`
	}{ConfigYAML: []byte("drive_refresh_token: new-refresh\n")})
	if !result.OK {
		t.Fatalf("credential reconfiguration failed: %v", result.Error)
	}
	service.driveAccess.mu.Lock()
	defer service.driveAccess.mu.Unlock()
	if service.driveAccess.value != "" || service.driveAccess.scopes != "" || !service.driveAccess.expires.IsZero() {
		t.Fatal("new Drive authorization would keep using the old readonly token")
	}
}
