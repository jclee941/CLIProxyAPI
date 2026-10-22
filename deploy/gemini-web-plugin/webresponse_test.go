package main

import (
	"errors"
	"fmt"
	"testing"
	"unicode/utf16"
)

func TestWebRepliesPreserveRPCFailures(t *testing.T) {
	for _, scenario := range []struct {
		rpc    int
		status int
		code   string
	}{
		{3, 400, "web_request_invalid"},
		{7, 403, "web_rpc_denied"},
		{8, 429, "web_quota_exhausted"},
		{9, 400, "web_failed_precondition"},
		{13, 502, "web_upstream_internal"},
		{14, 503, "web_upstream_unavailable"},
		{16, 401, "auth_error"},
		{99, 502, "web_upstream_error"},
	} {
		t.Run(fmt.Sprint(scenario.rpc), func(t *testing.T) {
			// Google's web client reads field 5 as google.rpc.Status, even
			// when the frame carries no payload in field 2.
			frame := jsonFixture(t, []any{[]any{"wrb.fr", "rpc", nil, nil, nil, []any{scenario.rpc}, "generic"}})
			raw := []byte(")]}'\n\n" + fmt.Sprintf("%d\n%s\n", len(utf16.Encode([]rune(string(frame)))), frame))
			_, textErr := webReplyText(raw)
			_, videoErr := webResponseFrames(raw)
			_, continuationErr := continuationFrame(continuationTurn{}, frame)
			_, rpcErr := decodeRPCFrames(raw, "rpc")
			for path, err := range map[string]error{
				"text": textErr, "video": videoErr, "continuation": continuationErr, "rpc": rpcErr,
			} {
				var public *publicError
				if !errors.As(err, &public) || public.Code != scenario.code || public.HTTPStatus != scenario.status {
					t.Errorf("%s: error = %v, want %s (%d)", path, err, scenario.code, scenario.status)
				}
				var authentication *AuthenticationFailure
				if errors.As(err, &authentication) != (scenario.rpc == 16) {
					t.Errorf("%s: authentication classification = %v", path, err)
				}
			}
		})
	}
}

func TestWebReplyDoesNotReturnPartialTextAfterRPCFailure(t *testing.T) {
	raw := generationFrame(t, "partial") + "\n" + `[["wrb.fr",null,null,null,null,[13],"generic"]]` + "\n"

	text, err := webReplyText([]byte(raw))

	if text != "" || safeCredentialCode(err) != "web_upstream_internal" {
		t.Fatalf("partial answer escaped a terminal error: text=%q err=%v", text, err)
	}
}

func TestWebRepliesRecognizeExecutorErrorFrames(t *testing.T) {
	frame := jsonFixture(t, []any{slots(11, map[int]any{0: "er", 2: "private upstream message", 5: 503, 9: 14})})
	_, textErr := webReplyText(frame)
	_, videoErr := webResponseFrames(frame)
	_, continuationErr := continuationFrame(continuationTurn{}, frame)
	for path, err := range map[string]error{"text": textErr, "video": videoErr, "continuation": continuationErr} {
		if safeCredentialCode(err) != "web_upstream_unavailable" {
			t.Errorf("%s: error = %v", path, err)
		}
	}
}

func TestRejectedSubmissionReleasesItsAccountWithoutResubmitting(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{rpcError: 8}
	continuationWeb(t, service, fixture)
	prepared := continuationReceipt(t, continuationCall(t, service, local, `{"geminiWebContinuation":{"action":"prepare"}}`))

	result := continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first"))

	if result.OK || result.Error == nil || result.Error.HTTPStatus != 429 {
		t.Fatalf("upstream rejection was not surfaced: %+v", result)
	}
	stored, err := service.sessions.read(local.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != localReady || stored.ContinuationActive != "" {
		t.Fatalf("rejected request still pins the account: state=%s active=%s", stored.State, stored.ContinuationActive)
	}
	turns, err := continuationTurns(stored)
	if err != nil {
		t.Fatal(err)
	}
	failed := turns[continuationKey(prepared.Token)]
	if failed.State != "failed" || failed.Error != "web_quota_exhausted" {
		t.Fatalf("rejection was not retained: %+v", failed)
	}
	continuationCall(t, service, local, submitContinuationBody(prepared.Token, "first"))
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 {
		t.Fatalf("rejected receipt submitted %d times", len(fixture.fields))
	}
}
