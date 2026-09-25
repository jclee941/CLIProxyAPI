package main

import (
	"regexp"
	"time"
)

const (
	credentialWorkerBudget   = time.Minute
	credentialFenceDuration  = 70 * time.Second
	maintenanceAuthCooldown  = 30 * time.Minute
	maintenanceReadyInterval = 30 * time.Minute
	maintainPath             = "/plugins/gemini-web/maintain"
	extensionArchive         = "gemini-web-login-companion.zip"
)

var (
	accountDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type credentialInspection struct {
	AccountSHA256 string `json:"account_sha256"`
	AuthUser      uint64 `json:"auth_user"`
}
