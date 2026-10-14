package main

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestVideoWireModeReconfiguresWithoutDrainingActiveRequests(t *testing.T) {
	for _, transition := range [][2]videoWireMode{
		{videoWireLegacy, videoWireWeb},
		{videoWireLegacy, videoWireAlternate},
		{videoWireWeb, videoWireLegacy},
	} {
		t.Run(string(transition[0])+"/"+string(transition[1]), func(t *testing.T) {
			// Given an open store and a request already admitted to its lifecycle.
			service, _ := loginFixture(t)
			service.config.VideoWireMode = transition[0]
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
					if event.Fields.State == "video_wire_mode" {
						logged = event.Fields.Reason
					}
				}
				return base(method, raw)
			}
			config := service.settings()
			config.VideoWireMode = transition[1]
			encoded, err := yaml.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			// When only the wire policy changes through actual reconfiguration.
			result := invoke(t, service, "plugin.reconfigure", struct {
				ConfigYAML []byte `json:"config_yaml"`
			}{encoded})
			// Then the policy applies and is logged without replacing live state.
			if !result.OK || service.settings().VideoWireMode != transition[1] {
				t.Fatalf("mode change failed: %v", result.Error)
			}
			if service.localStore() != store || service.leases.epoch != epoch {
				t.Fatal("wire policy replaced session state")
			}
			if logged != "mode="+string(transition[1]) {
				t.Fatalf("applied mode was not logged: %q", logged)
			}
		})
	}
}

func TestVideoModeReconfiguresWithoutDrainingActiveRequests(t *testing.T) {
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
			if event.Fields.State == "video_mode" {
				logged = event.Fields.Reason
			}
		}
		return base(method, raw)
	}
	reconfigure := func(mode int) bool {
		config := service.settings()
		config.VideoMode = mode
		encoded, err := yaml.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		result := invoke(t, service, "plugin.reconfigure", struct {
			ConfigYAML []byte `json:"config_yaml"`
		}{encoded})
		if !result.OK && (result.Error == nil || result.Error.Code != "invalid_plugin_config") {
			t.Fatalf("video_mode %d failed with %+v, want success or invalid_plugin_config", mode, result.Error)
		}
		return result.OK
	}
	// When only the default video mode changes through actual reconfiguration.
	if !reconfigure(8) || service.settings().VideoMode != 8 {
		t.Fatalf("video_mode 8 was not applied: %d", service.settings().VideoMode)
	}
	// Then it applies and is logged without replacing live state.
	if service.localStore() != store || service.leases.epoch != epoch {
		t.Fatal("video mode policy replaced session state")
	}
	if logged != "mode=8" {
		t.Fatalf("applied mode was not logged: %q", logged)
	}
	// And a negative mode is rejected while the applied one stays.
	if reconfigure(-1) || service.settings().VideoMode != 8 {
		t.Fatalf("video_mode -1 must be rejected and keep 8, now %d", service.settings().VideoMode)
	}
}
