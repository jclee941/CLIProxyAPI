package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type continuationFixtureTransport struct {
	base   http.RoundTripper
	origin string
}

func (transport continuationFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host == "gemini-web2api:8081" {
		clone := request.Clone(request.Context())
		clone.URL.Scheme = "https"
		clone.URL.Host = strings.TrimPrefix(transport.origin, "https://")
		return transport.base.RoundTrip(clone)
	}
	if request.URL.Host != strings.TrimPrefix(transport.origin, "https://") {
		return nil, failure(500, "fixture_external_network_denied")
	}
	return transport.base.RoundTrip(request)
}

type continuationWebFixture struct {
	mu             sync.Mutex
	fields         [][]any
	candidates     []any
	uploads        [][]byte
	uploadCookies  []string
	interrupted    bool
	missingHandles bool
	replyOnly      bool
	lateCandidate  bool
	rotate         bool
	expired        bool
	video          bool
	pending        bool
	wrongReply     bool
	// answer is the prose a non-video candidate carries; empty keeps "answer".
	answer       string
	beforeSubmit func()
	duringModels func()
}

func continuationWeb(t *testing.T, service *service, fixture *continuationWebFixture) *httptest.Server {
	t.Helper()
	var origin string
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		if request.URL.Path != "/RotateCookies" && request.URL.Path != "/video" && request.URL.Path != "/v1/account-models" && request.URL.Path != "/upload/" && request.URL.Path != "/upload/finalize" && !strings.HasPrefix(request.URL.Path, "/u/2/") {
			t.Errorf("wrong account prefix: %s", request.URL.Path)
		}
		switch {
		case request.URL.Path == "/v1/account-models":
			writeFixture(t, writer, `{"available":true,"models":[{"capability_id":"cap-flash","display_name":"3.8 Flash","mode":1}]}`)
		case strings.HasSuffix(request.URL.Path, "/app"):
			if fixture.expired {
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeIdentityFixture(t, writer)
		case strings.HasSuffix(request.URL.Path, "/StreamGenerate"):
			if fixture.beforeSubmit != nil {
				fixture.beforeSubmit()
			}
			if err := request.ParseForm(); err != nil {
				t.Error(err)
				return
			}
			var envelope []any
			if err := json.Unmarshal([]byte(request.Form.Get("f.req")), &envelope); err != nil {
				t.Error(err)
				return
			}
			encoded, ok := jsonField(envelope, 1).(string)
			if !ok {
				t.Error("missing fields")
				return
			}
			var fields []any
			if err := json.Unmarshal([]byte(encoded), &fields); err != nil {
				t.Error(err)
				return
			}
			fixture.fields = append(fixture.fields, fields)
			n := len(fixture.fields)
			answer := "answer"
			if fixture.answer != "" {
				answer = fixture.answer
			}
			candidate := slots(13, map[int]any{0: fmt.Sprintf("rc_%d", n), 1: []any{answer}, 8: []any{2}})
			if fixture.video {
				candidate = videoCandidate(origin+"/video", false).([]any)
				candidate[0] = fmt.Sprintf("rc_%d", n)
			}
			fixture.candidates = append(fixture.candidates, candidate)
			frame := slots(26, map[int]any{1: []any{"c_chat", fmt.Sprintf("r_%d", n)}, 4: []any{candidate}, 25: fmt.Sprintf("context_%d", n)})
			if fixture.missingHandles {
				frame[1] = nil
			}
			// What the product sends when it answers a turn and starts nothing:
			// a reply of its own, no conversation to continue, no candidate.
			if fixture.replyOnly {
				frame[1], frame[4] = []any{nil, fmt.Sprintf("r_%d", n)}, nil
			}
			// What the product sends when the render is still running as the
			// submit stream closes: the turn is named, the video is not there yet.
			if fixture.lateCandidate {
				frame[4] = nil
			}
			raw := string(jsonFixture(t, []any{[]any{"wrb.fr", nil, string(jsonFixture(t, frame))}})) + "\n"
			if fixture.interrupted {
				writer.Header().Set("Content-Length", strconv.Itoa(len(raw)+100))
			}
			writeFixture(t, writer, raw)
		case strings.HasSuffix(request.URL.Path, "/batchexecute"):
			if request.URL.Query().Get("rpcids") == accountCapabilityRPC {
				// What a real account does while it is answering this: the turn it
				// was running finishes, which moves the session out of submitting
				// and clears the active key.
				if fixture.duringModels != nil {
					fixture.duringModels()
				}
				writeFixture(t, writer, rpcEnvelope(t, accountCapabilityRPC, slots(16, map[int]any{14: 1000, 15: []any{slots(18, map[int]any{0: "cap-flash", 11: "3.8 Flash", 17: 1})}})))
				return
			}
			if request.URL.Query().Get("rpcids") != webVideoTurnsRPC {
				t.Error("unexpected RPC")
				return
			}
			entries := make([]any, 0, len(fixture.candidates))
			for index, candidate := range fixture.candidates {
				current := candidate
				if fixture.pending {
					current = slots(13, map[int]any{0: fmt.Sprintf("rc_%d", index+1), 1: []any{"https://googleusercontent.com/video_gen_chip/pending"}, 8: []any{1}})
				}
				reply := fmt.Sprintf("r_%d", index+1)
				if fixture.wrongReply {
					reply = "r_wrong"
				}
				entries = append(entries, slots(4, map[int]any{0: []any{nil, reply}, 3: []any{[]any{current}}}))
			}
			writeFixture(t, writer, rpcEnvelope(t, webVideoTurnsRPC, []any{entries}))
		case request.URL.Path == "/RotateCookies":
			if fixture.rotate {
				writeRotationFixture(writer, request)
				return
			}
			writer.WriteHeader(http.StatusOK)
		case request.URL.Path == "/video":
			writeFixture(t, writer, "0000ftypvideo")
		case request.URL.Path == "/upload/":
			writer.Header().Set("X-Goog-Upload-Url", origin+"/upload/finalize")
			writer.WriteHeader(http.StatusOK)
		case request.URL.Path == "/upload/finalize":
			content, err := io.ReadAll(request.Body)
			if err != nil {
				t.Error(err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			fixture.uploads = append(fixture.uploads, content)
			fixture.uploadCookies = append(fixture.uploadCookies, request.Header.Get("Cookie"))
			writeFixture(t, writer, "/uploaded/video")
		default:
			t.Errorf("unexpected path: %s", request.URL.Path)
			writer.WriteHeader(404)
		}
	}))
	origin = server.URL
	service.client = server.Client()
	service.client.Transport = continuationFixtureTransport{base: service.client.Transport, origin: origin}
	service.webOriginOverride, service.webUploadOverride, service.webRotateOverride = origin, origin, origin
	t.Cleanup(server.Close)
	return server
}

func submitContinuationBody(token, prompt string) string {
	return fmt.Sprintf(`{"geminiWebContinuation":{"action":"submit","token":%q},"contents":[{"role":"user","parts":[{"text":%q}]}]}`, token, prompt)
}

func TestContinuationFollowupUsesSameChatAndAccount(t *testing.T) {
	// Given a completed first turn, all calls through the real RPC dispatch and HTTP wire.
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{}
	continuationWeb(t, service, fixture)
	first := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	result := continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(first.Token, "first")))
	if result.State != "complete" {
		t.Fatalf("first state: %+v", result)
	}
	next := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare","token":"`+first.Token+`"}}`))
	// When a new prompt is submitted with the next receipt.
	result = continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(next.Token, "next")))
	// Then the upstream receives the first turn's exact metadata, not a random chat.
	if result.State != "complete" {
		t.Fatalf("followup: %+v", result)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 2 {
		t.Fatalf("submissions: %d", len(fixture.fields))
	}
	metadata := fixture.fields[1][2]
	if jsonField(metadata, 0) != "c_chat" || jsonField(metadata, 1) != "r_1" || jsonField(metadata, 2) != "rc_1" || jsonField(metadata, 9) != "context_1" {
		t.Fatalf("metadata: %#v", metadata)
	}
	if fixture.fields[0][59] == fixture.fields[1][59] {
		t.Fatal("per-turn request nonce was reused")
	}
}

func TestContinuationInterruptedReceiptSurvivesRestartWithoutResubmission(t *testing.T) {
	// Given a truncated HTTP response after a durable receipt frame.
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{interrupted: true}
	continuationWeb(t, service, fixture)
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))
	pending := continuationReceipt(t, continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first")))
	if pending.State != "pending" || pending.Error != "web_response_failed" {
		t.Fatalf("pending: %+v", pending)
	}
	path := service.sessions.directory.Name()
	if err := service.sessions.close(); err != nil {
		t.Fatal(err)
	}
	store, err := openSessionStore(path, sessionKeyFixture())
	if err != nil {
		t.Fatal(err)
	}
	service.sessions = store
	service.leases = credentialLeases{}
	t.Cleanup(func() {
		if err := store.close(); err != nil {
			t.Error(err)
		}
	})
	// When the same receipt is recovered after reopening the encrypted store.
	recovered := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"recover","token":"`+prepared.Token+`"}}`))
	// Then the existing candidate is returned, with no second StreamGenerate.
	if recovered.State != "complete" {
		t.Fatalf("recover: %+v", recovered)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 {
		t.Fatalf("resubmitted: %d", len(fixture.fields))
	}
}
