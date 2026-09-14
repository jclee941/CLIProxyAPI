package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	log "github.com/sirupsen/logrus"
)

// DoAuthStatus lists all stored authentication credentials and their current status.
func DoAuthStatus(cfg *config.Config) {
	store := sdkAuth.GetTokenStore()
	if fs, ok := store.(*sdkAuth.FileTokenStore); ok {
		fs.SetBaseDir(cfg.AuthDir)
	}

	auths, err := store.List(context.Background())
	if err != nil {
		log.Errorf("failed to list auth files: %v", err)
		return
	}

	if len(auths) == 0 {
		fmt.Println("No authentication credentials found.")
		fmt.Printf("Auth directory: %s\n", cfg.AuthDir)
		return
	}

	fmt.Printf("Auth directory: %s\n\n", cfg.AuthDir)

	fmt.Printf("%-4s  %-14s  %-30s  %-10s  %s\n", "#", "PROVIDER", "ACCOUNT", "STATUS", "EXPIRES")
	fmt.Println(strings.Repeat("-", 90))

	now := time.Now()
	for i, auth := range auths {
		provider := auth.Provider
		_, account := auth.AccountInfo()
		if account == "" {
			account = auth.Label
		}
		if account == "" {
			account = auth.ID
		}

		status := string(auth.Status)
		if auth.Disabled {
			status = "disabled"
		}

		expires := "-"
		if exp, ok := auth.ExpirationTime(); ok {
			if exp.Before(now) {
				expires = exp.Format("2006-01-02 15:04") + " (expired)"
			} else {
				remains := time.Until(exp).Truncate(time.Minute)
				expires = exp.Format("2006-01-02 15:04") + fmt.Sprintf(" (%s left)", remains)
			}
		}

		fmt.Printf("%-4d  %-14s  %-30s  %-10s  %s\n", i+1, provider, account, status, expires)
	}

	fmt.Printf("\nTotal: %d credential(s)\n", len(auths))
}
