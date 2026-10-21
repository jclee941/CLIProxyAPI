package main

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

// A text turn whose request names no thinking level takes the one
// text_thinking_levels sets for its model; a level the request names still
// wins, and a model the setting does not name sends none.
func TestTextModelTakesItsConfiguredThinkingLevelWhenTheRequestNamesNone(t *testing.T) {
	server := nativeWebServer(t, "a thought")
	service, record := nativeService(t, server)
	configure := func(levels map[string]string) {
		service.mu.Lock()
		service.config.TextThinkingLevels = levels
		service.mu.Unlock()
	}
	send := func(payload string) (any, any) {
		t.Helper()
		result := invoke(t, service, "executor.execute", executorRequest{
			AuthID: record.ID, AuthProvider: provider, Model: "gemini-app-3.8-flash",
			Format: "gemini", SourceFormat: "gemini", StorageJSON: jsonFixture(t, record),
			Payload: []byte(payload),
		})
		if !result.OK {
			t.Fatalf("text model turn failed: %+v", result.Error)
		}
		nativeGenerationBody.mu.Lock()
		body, selection := nativeGenerationBody.body, nativeGenerationBody.selection
		nativeGenerationBody.mu.Unlock()
		var header []any
		if err := json.Unmarshal([]byte(selection), &header); err != nil {
			t.Fatalf("selection header %s: %v", selection, err)
		}
		return jsonField(generationFields(t, body), 80), jsonField(header, 15)
	}
	unnamed := `{"contents":[{"role":"user","parts":[{"text":"think"}]}]}`
	named := `{"contents":[{"role":"user","parts":[{"text":"think"}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":"low"}}}`

	configure(map[string]string{"gemini-app-4-argon": "high"})
	if body, header := send(unnamed); body != nil || header != nil {
		t.Fatalf("a model the setting does not name sent level %v in the body and %v in the header", body, header)
	}
	configure(map[string]string{"gemini-app-3.8-flash": "high"})
	if body, header := send(unnamed); body != float64(2) || header != float64(2) {
		t.Fatalf("configured level: body %v, header %v, want extended (2)", body, header)
	}
	if body, header := send(named); body != float64(1) || header != float64(1) {
		t.Fatalf("named level: body %v, header %v, want standard (1)", body, header)
	}
}

func TestTextThinkingLevelsReconfigureWithoutDrainingActiveRequests(t *testing.T) {
	// Given an open store and a request already admitted to its lifecycle.
	service, _ := loginFixture(t)
	store, epoch := service.localStore(), service.leases.epoch
	if err := service.lifecycle.enter(); err != nil {
		t.Fatal(err)
	}
	defer service.lifecycle.leave()
	var logged string
	base := service.host
	service.host = func(method string, raw []byte) ([]byte, error) {
		if method == "host.log" {
			var event struct {
				Fields struct{ State, Reason string }
			}
			if err := json.Unmarshal(raw, &event); err != nil {
				t.Fatal(err)
			}
			if event.Fields.State == "text_thinking_levels" {
				logged = event.Fields.Reason
			}
		}
		return base(method, raw)
	}
	reconfigure := func(levels map[string]string) bool {
		config := service.settings()
		config.TextThinkingLevels = levels
		encoded, err := yaml.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		result := invoke(t, service, "plugin.reconfigure", struct {
			ConfigYAML []byte `json:"config_yaml"`
		}{encoded})
		if !result.OK && (result.Error == nil || result.Error.Code != "invalid_plugin_config") {
			t.Fatalf("text_thinking_levels %v failed with %+v, want success or invalid_plugin_config", levels, result.Error)
		}
		return result.OK
	}
	// When only the text thinking levels change through actual reconfiguration.
	if !reconfigure(map[string]string{"gemini-app-4-argon": " High "}) || service.settings().TextThinkingLevels["gemini-app-4-argon"] != "high" {
		t.Fatalf("the Argon level was not applied: %v", service.settings().TextThinkingLevels)
	}
	// Then it applies and is logged without replacing live state.
	if service.localStore() != store || service.leases.epoch != epoch {
		t.Fatal("text thinking levels replaced session state")
	}
	if logged != "gemini-app-4-argon=high" {
		t.Fatalf("applied levels were not logged: %q", logged)
	}
	// And a level no request could name is rejected while the applied one stays.
	if reconfigure(map[string]string{"gemini-app-4-argon": "deep"}) || service.settings().TextThinkingLevels["gemini-app-4-argon"] != "high" {
		t.Fatalf("an unknown level must be rejected and keep high, now %v", service.settings().TextThinkingLevels)
	}
}
