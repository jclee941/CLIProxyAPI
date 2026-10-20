package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistrationAcceptsHostMetadata(t *testing.T) {
	for _, method := range []string{"plugin.register", "plugin.reconfigure"} {
		for name, config := range map[string]string{
			"enabled":  "enabled: true\n",
			"priority": "priority: 4\n",
			"both":     "enabled: true\npriority: 4\n",
		} {
			t.Run(method+"/"+name, func(t *testing.T) {
				service := newService(nil)
				request := struct {
					ConfigYAML []byte `json:"config_yaml"`
				}{[]byte(config)}

				result := invoke(t, service, method, request)

				if !result.OK {
					t.Fatalf("host configuration rejected: %+v", result.Error)
				}
				if service.settings().DashboardPath != "/CLIProxyAPI/plugins/gemini-web/index.html" {
					t.Fatal("host metadata changed the provider configuration")
				}
			})
		}
	}
}

func TestRegistrationRejectsInvalidConfigurationAfterHostMetadata(t *testing.T) {
	for _, config := range []string{
		"enabled: [true]\npriority: 4\n",
		"enabled: true\npriority: [4]\n",
		"enabled: true\npriority: 4\nvault_token: forbidden\n",
	} {
		t.Run(config, func(t *testing.T) {
			service := newService(nil)
			request := struct {
				ConfigYAML []byte `json:"config_yaml"`
			}{[]byte(config)}

			result := invoke(t, service, "plugin.register", request)

			if result.OK || result.Error == nil || result.Error.Code != "invalid_plugin_config" {
				t.Fatalf("invalid configuration accepted: %+v", result)
			}
		})
	}
}

// productionShapedConfig mirrors the keys the live configuration carries; the
// native_generation line is the one the sidecar removal must keep accepting.
func productionShapedConfig(t *testing.T, nativeGeneration string) string {
	t.Helper()
	return "enabled: true\npriority: 10\n" +
		"session_dir: " + filepath.Join(t.TempDir(), "sessions") + "\n" +
		"manager_origin: https://manager.example\n" +
		"browser_extension_id: " + strings.Repeat("a", 32) + "\n" +
		"dashboard_path: /CLIProxyAPI/plugins/gemini-web/index.html\n" +
		"drive_client_id: drive-client.example\n" +
		"session_exchange_accounts:\n  - gemini-web-a.json\n" +
		nativeGeneration +
		"native_continuation: true\n"
}

func registerConfig(t *testing.T, config string) (*service, envelope) {
	t.Helper()
	t.Setenv("GEMINI_WEB_SESSION_KEY", sessionKeyFixture())
	service := newService(nil)
	result := invoke(t, service, "plugin.register", struct {
		ConfigYAML []byte `json:"config_yaml"`
	}{[]byte(config)})
	t.Cleanup(func() {
		if service.sessions != nil {
			if err := service.sessions.close(); err != nil {
				t.Error(err)
			}
		}
	})
	return service, result
}

func TestRegistrationLoadsProductionShapedConfiguration_withAndWithoutNativeGeneration(t *testing.T) {
	registrations := map[string]string{}
	for name, line := range map[string]string{"true": "native_generation: true\n", "omitted": ""} {
		t.Run(name, func(t *testing.T) {
			service, result := registerConfig(t, productionShapedConfig(t, line))

			if !result.OK {
				t.Fatalf("production configuration rejected: %+v", result.Error)
			}
			settings := service.settings()
			if !settings.NativeContinuation || !settings.HostEnabled || settings.HostPriority != 10 || settings.DriveClientID != "drive-client.example" || len(settings.SessionExchangeAccounts) != 1 || settings.LegacyNativeGeneration != nil {
				t.Fatalf("configuration not applied: %+v", settings)
			}
			registration := string(result.Result)
			if !strings.Contains(registration, `"scheduler":true`) || !strings.Contains(registration, `["gemini","openai-image","interactions"]`) {
				t.Fatalf("native continuation capabilities missing: %s", registration)
			}
			registrations[name] = registration
		})
	}
	if registrations["true"] != registrations["omitted"] {
		t.Fatal("native_generation changed the registration")
	}
}

func TestRegistrationRejectsDisabledNativeGeneration_becauseTheSidecarWasRemoved(t *testing.T) {
	_, result := registerConfig(t, productionShapedConfig(t, "native_generation: false\n"))

	if result.OK || result.Error == nil || result.Error.Code != "invalid_plugin_config" || result.Error.HTTPStatus != 400 || !strings.Contains(result.Error.Message, "sidecar path was removed") {
		t.Fatalf("native_generation: false accepted or unclear: %+v", result)
	}
}

func TestRegistrationReadsTheExtendDeclarationKeys(t *testing.T) {
	// Given: production-shaped configuration that turns both declarations on.
	service, result := registerConfig(t, productionShapedConfig(t, "extend_declaration: true\nextend_source_declaration: true\n"))

	// Then: the strict decoder accepts both keys and they apply.
	settings := service.settings()
	if !result.OK || !settings.ExtendDeclaration || !settings.ExtendSourceDeclaration {
		t.Fatalf("extend declaration keys not applied: %+v", result.Error)
	}
}
