package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVideoWireRegistration(t *testing.T) {
	for _, mode := range []string{"", "legacy", "web", "alternate", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			// Given a plugin configuration, including the omitted default.
			service := newService(nil)
			config := ""
			if mode != "" {
				config = "video_wire_mode: " + mode + "\n"
			}
			// When registration parses it at the configuration boundary.
			result := invoke(t, service, "plugin.register", struct {
				ConfigYAML []byte `json:"config_yaml"`
			}{[]byte(config)})
			// Then only the supported modes are accepted.
			if mode == "invalid" {
				if result.OK || result.Error.Code != "invalid_plugin_config" {
					t.Fatal("invalid mode was accepted")
				}
				return
			}
			want := videoWireMode(mode)
			if mode == "" {
				want = videoWireLegacy
			}
			if !result.OK || service.settings().VideoWireMode != want {
				t.Fatalf("mode=%q error=%v", service.settings().VideoWireMode, result.Error)
			}
		})
	}
}

func TestVideoWireAlternateIsStableAndBalanced(t *testing.T) {
	// Given a deterministic set of independent conversation keys.
	counts := map[videoWireMode]int{}
	for i := range 256 {
		key := fmt.Sprintf("conversation-%d", i)
		// When alternate selects each conversation repeatedly.
		variant := videoWireAlternate.variant(key, []int{16, 38})
		for range 5 {
			if again := videoWireAlternate.variant(key, []int{16, 38}); again != variant {
				t.Fatalf("unstable variant for %s", key)
			}
		}
		counts[variant]++
	}
	// Then both halves are populated without dependence on clocks or randomness.
	for _, variant := range []videoWireMode{videoWireLegacy, videoWireWeb} {
		if counts[variant] < 96 || counts[variant] > 160 {
			t.Fatalf("unbalanced split: %v", counts)
		}
	}
}

func TestVideoWireLegacyChatsAndRollback(t *testing.T) {
	for _, test := range []struct {
		name string
		turn continuationTurn
		mode videoWireMode
		want videoWireMode
	}{
		{"old chat", continuationTurn{Parent: "old"}, videoWireAlternate, videoWireLegacy},
		{"persisted web", continuationTurn{VideoWire: videoWireWeb}, videoWireAlternate, videoWireWeb},
		{"persisted legacy", continuationTurn{VideoWire: videoWireLegacy}, videoWireAlternate, videoWireLegacy},
		{"rollback", continuationTurn{VideoWire: videoWireWeb}, videoWireLegacy, videoWireLegacy},
		{"force web", continuationTurn{VideoWire: videoWireLegacy}, videoWireWeb, videoWireWeb},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Given a persisted chat and a current operator setting.
			encoded := jsonFixture(t, test.turn)
			var stored continuationTurn
			if err := strictJSON(encoded, &stored); err != nil {
				t.Fatal(err)
			}
			// When selecting a later turn after loading the durable record.
			got := stored.wireMode(test.mode).variant("different-turn", []int{16})
			// Then alternate inherits, while an explicit mode overrides.
			if got != test.want {
				t.Fatalf("variant=%s, want %s", got, test.want)
			}
			if got := stored.wireMode(test.mode).variant("different-turn", []int{8}); got != videoWireLegacy {
				t.Fatal("an account without flag 16 received web bytes")
			}
		})
	}
}

const legacyVideoBody = `[["a wave",0,null,null,null,null,0,null,null,[null,null,null,null,null,null,[[null,null,null,2]]]],["en"],["","","",null,null,null,null,null,null,""],null,null,null,[0],1,null,null,1,0,null,null,null,null,null,[[0]],0,null,null,null,null,null,null,null,null,1,null,null,[4],null,null,null,null,null,null,null,null,null,null,[1],null,null,null,null,null,null,null,11,null,null,null,0,[],[[17]],null,null,null,"request-id",null,[],null,null,null,null,null,0,1,null,null,null,null,null,null,null,null,null,null,1,2,null,null,null,null,null,null,null,null,null,null,0,null,null,null,null,0,null,1,null,null,null]`

func TestVideoWireRequestBytes(t *testing.T) {
	for _, test := range []struct {
		name     string
		mode     videoWireMode
		flags    []int
		variant  videoWireMode
		capacity int
	}{
		{"default", "", []int{16, 38}, videoWireLegacy, 1},
		{"legacy", videoWireLegacy, []int{16, 38}, videoWireLegacy, 1},
		{"legacy higher capacity", "", []int{8, 16}, videoWireLegacy, 2},
		{"web", videoWireWeb, []int{16, 38}, videoWireWeb, 3},
		{"web both flags", videoWireWeb, []int{8, 16}, videoWireWeb, 3},
		{"web missing flag", videoWireWeb, []int{38}, videoWireLegacy, 1},
		{"web legacy capacity", videoWireWeb, []int{8}, videoWireLegacy, 2},
		{"alternate missing flag", videoWireAlternate, []int{8}, videoWireLegacy, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Given a local HTTP boundary that captures the actual submission.
			var body, header string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
					return
				}
				var envelope []any
				if err := json.Unmarshal([]byte(r.Form.Get("f.req")), &envelope); err != nil {
					t.Error(err)
					return
				}
				var fields []any
				if err := json.Unmarshal([]byte(envelope[1].(string)), &fields); err != nil {
					t.Error(err)
					return
				}
				fields[59] = "request-id" // Only the per-request random nonce is normalized.
				body = string(jsonFixture(t, fields))
				header = r.Header.Get("x-goog-ext-525001261-jspb")
				if r.Header.Get("x-goog-ext-73010990-jspb") != "[0,0,0]" || r.Header.Get("x-goog-ext-73010989-jspb") != "[0]" {
					t.Error("unchanged extension headers were lost")
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			session := newWebSession(server.Client(), webCredential{}, server.URL)
			session.xsrf, session.videoWire = "fixture", test.mode
			session.diag = newVideoTurnDiag("fixture", "first", webAccount{}, capability{}, webFramingPortrait, 2, "en")
			// When a video submission goes through the real request encoder.
			_, err := session.submitVideo(t.Context(), "a wave", webAccount{CapacityFlags: test.flags}, capability{CapabilityID: "cap-flash", Mode: 1}, webFramingPortrait, 2, "en", nil)
			// Then every byte except the nonce is unchanged or one of the three approved fields.
			if err != nil {
				t.Fatal(err)
			}
			wantBody, caps := legacyVideoBody, "[4,5,6,8]"
			if test.variant == videoWireWeb {
				wantBody = strings.Replace(wantBody, "[4],", "[4,16],", 1)
				caps = "[4,5,6,8,16,4,5,6,8,16]"
			}
			wantHeader := fmt.Sprintf(`[1,null,null,null,"cap-flash",null,null,0,%s,null,null,%d,null,null,1]`, caps, test.capacity)
			if body != wantBody || header != wantHeader {
				t.Fatalf("request mismatch:\nbody=%s\nheader=%s", body, header)
			}
			reason := videoDiagFields(session.diag, "video")["reason"].(string)
			if !containsField(reason, "wire="+string(test.variant)) || !containsField(reason, fmt.Sprintf("capacity=%d", test.capacity)) {
				t.Fatalf("diagnostic does not match sent bytes: %s", reason)
			}
		})
	}
}
