package main

import "testing"

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
