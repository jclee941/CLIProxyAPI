package main

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSessionReconfigurationPreservesActiveCalls_whenProviderConfigIsUnchanged(t *testing.T) {
	for _, change := range []string{"none", "host_fields", "dashboard", "store", "origin", "extension", "key", "invalid_field"} {
		t.Run(change, func(t *testing.T) {
			service, _ := loginFixture(t)
			original := service.settings()
			store, keyHash := service.localStore(), service.sessionKeyHash
			config := original
			wantError := "session_busy"
			switch change {
			case "none":
				wantError = ""
			case "host_fields":
				config.HostEnabled, config.HostPriority = true, 19
				wantError = ""
			case "dashboard":
				config.DashboardPath = "/different/index.html"
			case "store":
				config.SessionDir += "-different"
			case "origin":
				config.ManagerOrigin = "https://different.example"
			case "extension":
				config.BrowserExtensionID = strings.Repeat("p", 32)
			case "key":
				t.Setenv("GEMINI_WEB_SESSION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
			case "invalid_field":
				wantError = "invalid_plugin_config"
			}
			configYAML, err := yaml.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			if change == "invalid_field" {
				configYAML = append(configYAML, []byte("unexpected_provider_field: true\n")...)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var unblock sync.Once
			finished := make(chan []byte, 1)
			base := service.host
			service.host = func(method string, raw []byte) ([]byte, error) {
				close(entered)
				<-release
				return base(method, raw)
			}
			t.Cleanup(func() { unblock.Do(func() { close(release) }); <-finished })
			raw := jsonFixture(t, managementRequest{Method: "GET", Path: accountsPath, HostCallbackID: "fixture-active"})
			go func() { finished <- service.handle(t.Context(), "management.handle", raw) }()
			<-entered

			result := invoke(t, service, "plugin.reconfigure", struct {
				ConfigYAML []byte `json:"config_yaml"`
			}{configYAML})

			if wantError == "" {
				if !result.OK {
					t.Fatalf("unchanged provider configuration rejected: %+v", result.Error)
				}
				var registration struct {
					SchemaVersion int `json:"schema_version"`
					Capabilities  struct {
						ManagementAPI bool `json:"management_api"`
						ModelProvider bool `json:"model_provider"`
					} `json:"capabilities"`
				}
				if err := json.Unmarshal(result.Result, &registration); err != nil {
					t.Fatal(err)
				}
				if registration.SchemaVersion != 6 || !registration.Capabilities.ManagementAPI || !registration.Capabilities.ModelProvider {
					t.Fatal("no-op lost registered capabilities")
				}
			} else if result.OK || result.Error.Code != wantError {
				t.Fatalf("changed/invalid configuration: %+v; want %s", result.Error, wantError)
			}
			if !reflect.DeepEqual(service.settings(), original) || service.localStore() != store || service.sessionKeyHash != keyHash || service.leases.epoch != 0 {
				t.Fatal("reconfigure mutated active configuration/store/credential epoch")
			}
		})
	}
}
