package watcher

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"gopkg.in/yaml.v3"
)

// A save that lands while a reload runs must not be recorded as applied: the
// next check has to load it, or the runtime keeps the older config until the
// next edit.
func TestReloadConfigIfChangedReloadsSaveThatLandsDuringReload(t *testing.T) {
	tmpDir := t.TempDir()
	authDir := filepath.Join(tmpDir, "auth")
	if errMkdir := os.MkdirAll(authDir, 0o755); errMkdir != nil {
		t.Fatalf("create auth dir: %v", errMkdir)
	}
	configPath := filepath.Join(tmpDir, "config.yaml")
	writeConfig := func(port int) {
		data, errMarshal := yaml.Marshal(&config.Config{
			Port:               port,
			AuthDir:            authDir,
			CredentialInFlight: config.DefaultCredentialInFlightConfig(),
		})
		if errMarshal != nil {
			t.Fatalf("marshal config: %v", errMarshal)
		}
		if errWrite := os.WriteFile(configPath, data, 0o644); errWrite != nil {
			t.Fatalf("write config: %v", errWrite)
		}
	}
	writeConfig(8080)

	var applied []int
	w := &Watcher{configPath: configPath, authDir: authDir}
	w.reloadCallback = func(cfg *config.Config) {
		applied = append(applied, cfg.Port)
		if len(applied) == 1 {
			// A concurrent save replaces the file after this reload read it.
			writeConfig(9090)
		}
	}

	w.reloadConfigIfChanged()
	w.reloadConfigIfChanged()

	if len(applied) != 2 || applied[1] != 9090 {
		t.Fatalf("applied ports = %v, want 8080 then the concurrent save 9090", applied)
	}
}
