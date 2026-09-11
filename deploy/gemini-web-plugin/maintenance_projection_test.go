package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestMaintenanceHostSyncChangesReferenceOnlyWatcherContent(t *testing.T) {
	service, _, record, _ := maintenanceFixture(t)
	initial, err := authFromRecord(*record)
	if err != nil {
		t.Fatal(err)
	}
	previous := initial.StorageJSON
	baseHost := service.host
	var savedRevision uint64
	service.host = func(method string, raw []byte) ([]byte, error) {
		if method != "host.auth.save" {
			return baseHost(method, raw)
		}
		var request callbackRequest
		if json.Unmarshal(raw, &request) != nil {
			t.Fatal("invalid host save")
		}
		if bytes.Equal(previous, request.JSON) {
			t.Fatal("host watcher will skip unchanged auth content after a referenced cookie rotates")
		}
		var saved struct {
			SessionRevision uint64 `json:"session_revision"`
		}
		if json.Unmarshal(request.JSON, &saved) != nil || saved.SessionRevision != savedRevision+1 {
			t.Fatal("each host resync must advance the non-secret credential revision")
		}
		parsed, err := service.parseStorage(request.JSON, true)
		if err != nil || parsed.TokenRef != record.TokenRef || parsed.Disabled != record.Disabled {
			t.Fatal("revision update changed credential ownership or disabled state")
		}
		*record = parsed
		previous = bytes.Clone(request.JSON)
		savedRevision = saved.SessionRevision
		return []byte(`{"ok":true,"result":{"name":"saved"}}`), nil
	}
	for range 2 {
		if err := service.syncCredentialHost("scope-list", *record); err != nil {
			t.Fatal(err)
		}
	}
}
