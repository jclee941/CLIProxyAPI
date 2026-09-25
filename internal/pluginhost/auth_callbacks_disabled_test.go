package pluginhost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestHostAuthSaveCallbackPreservesDisabledState(t *testing.T) {
	tests := []struct {
		name             string
		incoming         string
		existing         bool
		existingDisabled bool
		wantDisabled     bool
	}{
		{name: "new disabled", incoming: `"disabled":true`, wantDisabled: true},
		{name: "existing active becomes disabled", incoming: `"disabled":true`, existing: true, wantDisabled: true},
		{name: "relogin cannot enable", incoming: `"disabled":false`, existing: true, existingDisabled: true, wantDisabled: true},
		{name: "relogin omission preserves disabled", existing: true, existingDisabled: true, wantDisabled: true},
		{name: "new explicit active", incoming: `"disabled":false`},
		{name: "new default active"},
		{name: "existing active stays active", incoming: `"disabled":false`, existing: true},
		{name: "noncanonical field is not disabled", incoming: `"Disabled":true`},
		{name: "canonical field wins", incoming: `"disabled":true,"Disabled":false`, wantDisabled: true},
		{name: "duplicate fields cannot enable", incoming: `"disabled":false,"disabled":false`, existing: true, existingDisabled: true, wantDisabled: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authDir := t.TempDir()
			host := New()
			host.runtimeConfig = &config.Config{AuthDir: authDir}
			manager := coreauth.NewManager(nil, nil, nil)
			host.SetAuthManager(manager)
			const name = "saved.json"
			if test.existing {
				status := coreauth.StatusActive
				if test.existingDisabled {
					status = coreauth.StatusDisabled
				}
				_, errRegister := manager.Register(t.Context(), &coreauth.Auth{
					ID: name, FileName: name, Provider: "demo",
					Disabled: test.existingDisabled, Status: status,
				})
				if errRegister != nil {
					t.Fatalf("register auth: %v", errRegister)
				}
			}
			incoming := `{"type":"demo","account_number":9007199254740993`
			if test.incoming != "" {
				incoming += "," + test.incoming
			}
			request, errMarshal := json.Marshal(pluginapi.HostAuthSaveRequest{
				Name: "  " + name + "  ", JSON: json.RawMessage(incoming + "}"),
			})
			if errMarshal != nil {
				t.Fatalf("marshal request: %v", errMarshal)
			}

			rawResponse, errCall := host.callFromPlugin(t.Context(), pluginabi.MethodHostAuthSave, request)

			if errCall != nil {
				t.Fatalf("host.auth.save: %v", errCall)
			}
			response, errDecode := decodeRPCEnvelope[pluginapi.HostAuthSaveResponse](rawResponse)
			if errDecode != nil {
				t.Fatalf("decode response: %v", errDecode)
			}
			if response.Name != name || response.Path != filepath.Join(authDir, name) {
				t.Fatalf("save response = %#v, want canonical name and path", response)
			}
			auth, ok := manager.GetByID(name)
			if !ok {
				t.Fatal("saved auth missing at canonical ID")
			}
			wantStatus := coreauth.StatusActive
			if test.wantDisabled {
				wantStatus = coreauth.StatusDisabled
			}
			if auth.Disabled != test.wantDisabled || auth.Status != wantStatus {
				t.Errorf("runtime disabled/status = %v/%s, want %v/%s", auth.Disabled, auth.Status, test.wantDisabled, wantStatus)
			}
			if test.wantDisabled && auth.Metadata["disabled"] != true {
				t.Errorf("runtime metadata disabled = %v, want true", auth.Metadata["disabled"])
			}
			data, errRead := os.ReadFile(response.Path)
			if errRead != nil {
				t.Fatalf("read saved file: %v", errRead)
			}
			var persistedDisabled bool
			var fields map[string]json.RawMessage
			if errDecode := json.Unmarshal(data, &fields); errDecode != nil {
				t.Fatalf("decode saved fields: %v", errDecode)
			}
			if disabled, exists := fields["disabled"]; exists {
				if errDecode := json.Unmarshal(disabled, &persistedDisabled); errDecode != nil {
					t.Fatalf("decode saved disabled: %v", errDecode)
				}
			}
			if persistedDisabled != test.wantDisabled {
				t.Errorf("persisted disabled = %v, want %v", persistedDisabled, test.wantDisabled)
			}
			if string(fields["account_number"]) != "9007199254740993" {
				t.Errorf("unrelated account_number changed: %s", fields["account_number"])
			}
		})
	}
}

func TestHostAuthSaveCallbackPreservesSourceWhenValidationFails(t *testing.T) {
	for _, invalid := range []string{
		`"disabled":"true"`, `"disabled":"false"`, `"disabled":1`,
		`"disabled":null`, `"disabled":[]`, `"disabled":{}`,
		`"disabled":true,"weight":1.5`,
	} {
		t.Run(invalid, func(t *testing.T) {
			authDir := t.TempDir()
			path := filepath.Join(authDir, "saved.json")
			const original = `{"type":"demo","disabled":true,"note":"original"}`
			if errWrite := os.WriteFile(path, []byte(original), 0o600); errWrite != nil {
				t.Fatalf("write original: %v", errWrite)
			}
			host := New()
			host.runtimeConfig = &config.Config{AuthDir: authDir}
			manager := coreauth.NewManager(nil, nil, nil)
			host.SetAuthManager(manager)
			if _, errRegister := manager.Register(t.Context(), &coreauth.Auth{
				ID: "saved.json", Provider: "demo", Disabled: true, Status: coreauth.StatusDisabled,
			}); errRegister != nil {
				t.Fatalf("register auth: %v", errRegister)
			}
			request := []byte(`{"name":"saved.json","json":{"type":"demo",` + invalid + `}}`)

			_, errCall := host.callFromPlugin(t.Context(), pluginabi.MethodHostAuthSave, request)

			if errCall == nil {
				t.Error("host.auth.save accepted invalid auth metadata")
			}
			data, errRead := os.ReadFile(path)
			if errRead != nil {
				t.Fatalf("read original: %v", errRead)
			}
			if string(data) != original {
				t.Errorf("source changed after validation failure: %s", data)
			}
			auth, ok := manager.GetByID("saved.json")
			if !ok || !auth.Disabled || auth.Status != coreauth.StatusDisabled {
				t.Error("runtime disabled state changed after validation failure")
			}
		})
	}
}
