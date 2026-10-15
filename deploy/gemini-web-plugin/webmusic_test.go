package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

var (
	musicTrack = []byte("ID3\x04\x00\x00\x00\x00\x00\x00fixture-track")
	musicCover = []byte("\x00\x00\x00\x18ftypmp42fixture-cover")
)

type musicWebFixture struct {
	mu      sync.Mutex
	noMusic bool
	// partial is how many times the track answers 206 before it is complete.
	partial int
	prompts []string
	fetches int
}

func musicWebServer(t *testing.T, fixture *musicWebFixture) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/app"):
			writeFixture(t, writer, `{"SNlM0e":"xsrf","cfb2h":"build","FdrFJe":"sid"}`)
		case strings.HasSuffix(request.URL.Path, "/batchexecute"):
			writeCapabilities(t, writer, "3.8 Flash")
		case strings.HasSuffix(request.URL.Path, "/StreamGenerate"):
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				t.Error(err)
			}
			prompt, _ := jsonField(generationFields(t, string(raw)), 0, 0).(string)
			fixture.mu.Lock()
			fixture.prompts = append(fixture.prompts, prompt)
			noMusic := fixture.noMusic
			fixture.mu.Unlock()
			frame := musicFrame(t, server.URL, false)
			if noMusic {
				frame = generationFrame(t, "I can't make music right now")
			}
			writeFixture(t, writer, ")]}'\n"+frame+"\n")
		case request.URL.Path == "/track.mp3":
			fixture.mu.Lock()
			fixture.fetches++
			partial := fixture.partial > 0
			if partial {
				fixture.partial--
			}
			fixture.mu.Unlock()
			if partial {
				writer.WriteHeader(http.StatusPartialContent)
				return
			}
			writer.Header().Set("Content-Type", "audio/mpeg")
			writeFixture(t, writer, string(musicTrack))
		case request.URL.Path == "/cover.mp4":
			writer.Header().Set("Content-Type", "video/mp4")
			writeFixture(t, writer, string(musicCover))
		default:
			t.Errorf("unexpected request %s", request.URL.String())
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// musicFrame is a reply that made a track: a placeholder link in the text, and
// the mp3 and cover video pairs in field 86 of the rich content, positional or
// in the sparse bundle.
func musicFrame(t *testing.T, origin string, sparse bool) string {
	t.Helper()
	media := []any{
		[]any{nil, slots(8, map[int]any{7: []any{origin + "/track.jpg", origin + "/track.mp3"}})},
		[]any{nil, slots(8, map[int]any{7: []any{origin + "/cover.jpg", origin + "/cover.mp4"}})},
	}
	rich := slots(87, map[int]any{86: media})
	if sparse {
		rich = []any{nil, map[string]any{"87": media}}
	}
	candidate := slots(13, map[int]any{0: "rc_song", 1: []any{"Here is your track.\n\nhttp://googleusercontent.com/music_generation_content/0\n"}, 12: rich})
	inner, err := json.Marshal(slots(26, map[int]any{1: []any{"c_song", "r_song"}, 4: []any{candidate}}))
	if err != nil {
		t.Fatal(err)
	}
	frame, err := json.Marshal([]any{[]any{"wrb.fr", nil, string(inner)}})
	if err != nil {
		t.Fatal(err)
	}
	return string(frame)
}

func musicCall(t *testing.T, fixture *musicWebFixture) envelope {
	t.Helper()
	server := musicWebServer(t, fixture)
	service, record := nativeService(t, server)
	service.client = server.Client()
	service.continuationWait = func(context.Context) error { return nil }
	return invoke(t, service, "executor.execute", executorRequest{
		AuthID: record.ID, AuthProvider: provider, Model: webMusicModel,
		Format: "gemini", SourceFormat: "gemini", StorageJSON: jsonFixture(t, record),
		Payload: []byte(`{"contents":[{"role":"user","parts":[{"text":"a calm piano piece"}]}]}`),
	})
}

func TestMusicTurnReturnsTheTrackAndCoverInline(t *testing.T) {
	fixture := &musicWebFixture{partial: 2}
	parts := imageParts(t, musicCall(t, fixture))
	if len(parts) != 3 || parts[0].Text != "Here is your track." {
		t.Fatalf("parts = %+v", parts)
	}
	if parts[1].InlineData.MIMEType != "audio/mpeg" || parts[1].InlineData.Data != base64.StdEncoding.EncodeToString(musicTrack) {
		t.Fatalf("track part = %+v", parts[1])
	}
	if parts[2].InlineData.MIMEType != "video/mp4" || parts[2].InlineData.Data != base64.StdEncoding.EncodeToString(musicCover) {
		t.Fatalf("cover part = %+v", parts[2])
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if !slices.Equal(fixture.prompts, []string{"Generate music: a calm piano piece"}) || fixture.fetches != 3 {
		t.Fatalf("prompts %q, track fetches %d", fixture.prompts, fixture.fetches)
	}
}

func TestMusicReplyReadsTheSparseBundle(t *testing.T) {
	reply, err := webParseMusicReply([]byte(")]}'\n" + musicFrame(t, "https://media.example", true) + "\n"))
	if err != nil || reply.Audio != "https://media.example/track.mp3" || reply.Video != "https://media.example/cover.mp4" {
		t.Fatalf("reply = %+v, err = %v", reply, err)
	}
}

func TestMusicTurnWithoutATrackIsRefused(t *testing.T) {
	result := musicCall(t, &musicWebFixture{noMusic: true})
	if result.OK || result.Error == nil || result.Error.Code != "no_music_generated" || !strings.Contains(result.Error.Message, "I can't make music right now") {
		t.Fatalf("result = %+v", result)
	}
}
