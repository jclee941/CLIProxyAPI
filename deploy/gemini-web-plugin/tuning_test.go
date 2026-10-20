package main

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestTuningKeysOverrideTheirDefaults(t *testing.T) {
	var defaults tuning
	if defaults.roomTurns() != roomTurns || defaults.roomCeiling() != firstEntryRoomCeiling || defaults.videoLimitHold() != videoLimitHold || defaults.textThinkingSlot() != webThinkingDefault {
		t.Fatalf("zero tuning: room %d ceiling %v hold %v slot %d", defaults.roomTurns(), defaults.roomCeiling(), defaults.videoLimitHold(), defaults.textThinkingSlot())
	}
	tuned := tuning{RouteRoomCeilingPercent: 95, VideoLimitHoldSeconds: 600, RoomTurns: 5, CredentialWaitSeconds: 30, ContinuationRetention: 4}
	if tuned.roomCeiling() != 0.95 || tuned.videoLimitHold() != 10*time.Minute || tuned.roomTurns() != 5 || tuned.credentialWait() != 30*time.Second || tuned.continuationRetention() != 4 {
		t.Fatalf("tuned: ceiling %v hold %v room %d wait %v retention %d", tuned.roomCeiling(), tuned.videoLimitHold(), tuned.roomTurns(), tuned.credentialWait(), tuned.continuationRetention())
	}
}

func TestTuningBoundsAnInteractionRequest(t *testing.T) {
	body := []byte(`{"model":"` + interactionOmniModel + `","input":"a cat","generation_config":{"room_videos":5}}`)
	if _, _, err := (tuning{}).parseInteraction(body); safeCredentialCode(err) != "interaction_room_videos_unsupported" {
		t.Fatalf("five videos on the default room: %v", err)
	}
	if _, _, err := (tuning{RoomTurns: 5}).parseInteraction(body); err != nil {
		t.Fatalf("five videos on a five-turn room: %v", err)
	}
	if _, _, err := (tuning{RoomTurns: 5, InteractionInputBytes: 16}).parseInteraction(body); err == nil {
		t.Fatal("a request over the tuned input limit was accepted")
	}
}

func TestRegisterAppliesTuningWithoutReplacingTheSessionStore(t *testing.T) {
	svc, _ := loginFixture(t)
	store := svc.localStore()
	config := svc.settings()
	reconfigure := func() envelope {
		raw, err := yaml.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		return invoke(t, svc, "plugin.reconfigure", struct {
			ConfigYAML []byte `json:"config_yaml"`
		}{raw})
	}
	config.Tuning.RoomTurns = 4
	if reply := reconfigure(); !reply.OK {
		t.Fatalf("tuned reconfigure: %+v", reply.Error)
	}
	if svc.tuning().roomTurns() != 4 {
		t.Fatalf("room turns = %d, want 4", svc.tuning().roomTurns())
	}
	if svc.localStore() != store {
		t.Fatal("a tuning change replaced the session store")
	}
	config.Tuning.RoomTurns = -1
	if reply := reconfigure(); reply.OK || reply.Error.Code != "invalid_plugin_config" {
		t.Fatalf("negative room turns: %+v", reply)
	}
	if svc.tuning().roomTurns() != 4 {
		t.Fatalf("a rejected config changed room turns to %d", svc.tuning().roomTurns())
	}
}
