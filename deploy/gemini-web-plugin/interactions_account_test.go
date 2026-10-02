package main

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// Every Interactions response a caller reads names the account that handled the
// turn by a readable name taken from its label, with the account's last quota
// reading beside it. These tests read the machine-consumed fields, never the
// wording around them.

var readableAccountPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

var shortAccountPattern = regexp.MustCompile(`^[0-9a-f]{6}$`)

const namedAccount = "someone"

func namedAccountService(t *testing.T) *service {
	t.Helper()
	service := newService(nil)
	service.rememberAccount(storageRecord{ID: "account.json", Label: "Someone@Example.com"})
	return service
}

func TestAccountNameIsTheLabelAndHexOnlyWhenNoLabelIsKnown(t *testing.T) {
	service := newService(nil)
	file := "gemini-web-0123456789abcdef0123456789abcdef.json"
	if got := service.accountName(file); !shortAccountPattern.MatchString(got) || got != diagAccount(file) {
		t.Fatalf("an account with no label is named %q, want the short id %q", got, diagAccount(file))
	}
	service.rememberAccount(storageRecord{ID: file, Label: "Sunmin938A@gmail.com"})
	got := service.accountName(file)
	if got != "sunmin938a" || !readableAccountPattern.MatchString(got) {
		t.Fatalf("a labelled account is named %q, want sunmin938a", got)
	}
	if got := service.accountName("  "); got != "" {
		t.Fatalf("no account chosen must give no name, got %q", got)
	}
	if display := service.accountDisplay(""); display.Name != "" || display.Usage != nil {
		t.Fatalf("no account chosen must give an empty display, got %+v", display)
	}
	service.rememberAccount(storageRecord{ID: file, Label: "\u00ed\u0095\u009c"})
	if got := service.accountName(file); !shortAccountPattern.MatchString(got) {
		t.Fatalf("a label with nothing usable falls back to the short id, got %q", got)
	}
}

func accountOfInteraction(t *testing.T, payload []byte) string {
	t.Helper()
	var body struct {
		Account string `json:"account"`
		Error   struct {
			Account string `json:"account"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("unreadable interaction: %s", payload)
	}
	return body.Account
}

func accountOfEnvelopeResult(t *testing.T, result envelope) string {
	t.Helper()
	if !result.OK {
		t.Fatalf("interaction failed: %+v", result.Error)
	}
	var response struct{ Payload []byte }
	if err := json.Unmarshal(result.Result, &response); err != nil {
		t.Fatal(err)
	}
	return accountOfInteraction(t, response.Payload)
}

func accountOfEnvelopeError(t *testing.T, result envelope) string {
	t.Helper()
	if result.OK || result.Error == nil {
		t.Fatal("the turn was expected to fail")
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Account string `json:"account"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(result.Error.Message), &body); err != nil {
		t.Fatalf("the failure was not the interaction error object: %q", result.Error.Message)
	}
	return body.Error.Account
}

func TestRenderedInteractionNamesTheAccountForEveryState(t *testing.T) {
	service := namedAccountService(t)
	want := namedAccount
	video := []byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"video/mp4","data":"AAAA"}}]}}]}`)
	for name, view := range map[string]continuationView{
		"pending": {Token: "t", State: "pending"},
		"failed":  {Token: "t", State: "outcome_unknown", Error: "no_video_generated"},
		"refused": {Token: "t", State: "outcome_unknown", Error: "quota", ErrorMessage: "quota"},
	} {
		result, err := service.renderInteraction("account.json", continuationResult{}, view)
		if err != nil {
			t.Fatal(name, err)
		}
		if got := accountOfInteraction(t, result.(continuationResult).Payload); got != want {
			t.Fatalf("%s: account = %q, want %q", name, got, want)
		}
	}
	result, err := service.renderInteraction("account.json", continuationResult{Payload: video}, continuationView{Token: "t", State: "complete"})
	if err != nil {
		t.Fatal(err)
	}
	if got := accountOfInteraction(t, result.(continuationResult).Payload); got != want {
		t.Fatalf("complete: account = %q, want %q", got, want)
	}
	for _, leaked := range []string{"account.json", "example.com", "Example"} {
		if bytes.Contains(result.(continuationResult).Payload, []byte(leaked)) {
			t.Fatalf("%q leaked into the response", leaked)
		}
	}
}

func TestInteractionFailureNamesTheAccountOnlyWhenOneWasChosen(t *testing.T) {
	service := namedAccountService(t)
	named := interactionAccountFailure(service.accountDisplay("account.json"), failure(409, "session_busy"))
	var public *publicError
	if !asPublicError(named, &public) {
		t.Fatal("not a public error")
	}
	if public.HTTPStatus != 409 || public.Code != "session_busy" {
		t.Fatalf("status and code must not change: %+v", public)
	}
	var body struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal([]byte(public.Message), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error["account"] != namedAccount || body.Error["code"] != "session_busy" {
		t.Fatalf("error object = %v", body.Error)
	}
	// A refusal raised before the scheduler picked anyone has no account to name.
	if err := interactionFailure(failure(409, "account_unavailable")); strings.Contains(err.Error(), `"account"`) {
		t.Fatalf("an unchosen account was named: %s", err.Error())
	}
}

func TestExecutorFailureAfterTheAccountWasChosenNamesIt(t *testing.T) {
	service, local := continuationFixture(t)
	auth, err := authFromRecord(local.Target)
	if err != nil {
		t.Fatal(err)
	}
	request := executorRequest{AuthID: local.Target.ID, AuthProvider: provider, Model: interactionOmniModel, Format: "interactions", SourceFormat: "interactions", StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, Payload: []byte(`{"model":"gemini-omni-1.1-flash","input":"x"}`)}
	// No caller scope: the executor refuses after the host has chosen the account.
	result := invoke(t, service, "executor.execute", request)
	if got := accountOfEnvelopeError(t, result); got != accountDisplayName(local.Target.Label) {
		t.Fatalf("failure account = %q, want %q", got, accountDisplayName(local.Target.Label))
	}
}

func TestFreshExtendedAndRetrievedTurnsNameTheAccount(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	want := accountDisplayName(local.Target.Label)

	fresh := interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`)
	if got := accountOfEnvelopeResult(t, fresh); got != want {
		t.Fatalf("fresh account = %q, want %q", got, want)
	}
	first := interactionID(t, fresh)
	extended := interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"continue","previous_interaction_id":"`+first+`","generation_config":{"video_config":{"task":"extend"}}}`)
	if got := accountOfEnvelopeResult(t, extended); got != want {
		t.Fatalf("extension account = %q, want %q", got, want)
	}
	get := interactionExecutorRequest(t, local, `{"model":"gemini-omni-1.1-flash","id":"`+first+`"}`)
	get.Alt = interactionRetrieveAlt
	snapshot, err := service.executeInteraction(t.Context(), get)
	if err != nil {
		t.Fatal(err)
	}
	if got := accountOfInteraction(t, snapshot.(continuationResult).Payload); got != want {
		t.Fatalf("GET snapshot account = %q, want %q", got, want)
	}
}

func TestDeclinedFreshAndRetriedTurnsNameTheAccountOnTheFailure(t *testing.T) {
	service, local := continuationFixture(t)
	fixture := &continuationWebFixture{video: false}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	want := accountDisplayName(local.Target.Label)

	declined := interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`)
	if got := accountOfEnvelopeError(t, declined); got != want {
		t.Fatalf("declined account = %q, want %q", got, want)
	}
}

func TestStreamEventsNameTheAccount(t *testing.T) {
	service := namedAccountService(t)
	calls := interactionStreamCalls(service)
	operation := &interactionOperation{account: "account.json", done: make(chan struct{}), err: webNoVideo("Daily video limit reached.")}
	close(operation.done)
	t.Cleanup(func() {
		if err := service.shutdownSessions(); err != nil {
			t.Error(err)
		}
	})
	want := namedAccount

	if _, err := service.subscribeInteraction("s", "tok", 0, operation); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for {
		call := interactionAwait(t, calls)
		if call.Method == "host.stream.close" {
			break
		}
		name, data := sseEvent(t, call.Payload)
		switch name {
		case "interaction.created", "interaction.failed":
			var event struct {
				Interaction struct {
					Account string `json:"account"`
				} `json:"interaction"`
			}
			if err := json.Unmarshal(data, &event); err != nil {
				t.Fatal(err)
			}
			if event.Interaction.Account != want {
				t.Fatalf("%s account = %q, want %q", name, event.Interaction.Account, want)
			}
		case "error":
			var event struct {
				Error struct {
					Account string `json:"account"`
				} `json:"error"`
				Interaction struct {
					Account string `json:"account"`
				} `json:"interaction"`
			}
			if err := json.Unmarshal(data, &event); err != nil {
				t.Fatal(err)
			}
			if event.Error.Account != want || event.Interaction.Account != want {
				t.Fatalf("error event accounts = %q/%q, want %q", event.Error.Account, event.Interaction.Account, want)
			}
		}
		seen[name] = true
	}
	for _, name := range []string{"interaction.created", "interaction.failed", "error"} {
		if !seen[name] {
			t.Fatalf("no %s event was emitted: %v", name, seen)
		}
	}
}

func TestStreamedFailedAndCompletedResultsNameTheAccount(t *testing.T) {
	want := namedAccount
	video := []byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"video/mp4","data":"AAAA"}}]}}]}`)
	for name, view := range map[string]continuationView{
		"quota":     {Token: "tok", State: "outcome_unknown", Error: "video_quota_exhausted", ErrorMessage: "video_quota_exhausted"},
		"completed": {Token: "tok", State: "complete"},
	} {
		service := namedAccountService(t)
		calls := interactionStreamCalls(service)
		rendered, err := service.renderInteraction("account.json", continuationResult{Payload: video}, view)
		if err != nil {
			t.Fatal(err)
		}
		operation := &interactionOperation{account: "account.json", done: make(chan struct{}), result: rendered.(continuationResult)}
		close(operation.done)
		if _, err := service.subscribeInteraction("s", "tok", 0, operation); err != nil {
			t.Fatal(err)
		}
		accounts := map[string]string{}
		for {
			call := interactionAwait(t, calls)
			if call.Method == "host.stream.close" {
				break
			}
			eventName, data := sseEvent(t, call.Payload)
			if eventName == "done" {
				continue
			}
			var event struct {
				Interaction struct {
					Account string `json:"account"`
				} `json:"interaction"`
				Error struct {
					Account string `json:"account"`
				} `json:"error"`
			}
			if err := json.Unmarshal(data, &event); err != nil {
				continue
			}
			if event.Interaction.Account != "" {
				accounts[eventName] = event.Interaction.Account
			}
			if event.Error.Account != "" {
				accounts[eventName+".error"] = event.Error.Account
			}
		}
		terminal := map[string]string{"quota": "interaction.failed", "completed": "interaction.completed"}[name]
		if accounts[terminal] != want || accounts["interaction.created"] != want {
			t.Fatalf("%s: accounts = %v, want %q on created and %s", name, accounts, want, terminal)
		}
		if name == "quota" && accounts["error.error"] != want {
			t.Fatalf("quota: the error event did not name the account: %v", accounts)
		}
		if err := service.shutdownSessions(); err != nil {
			t.Error(err)
		}
	}
}

func sseEvent(t *testing.T, payload []byte) (string, []byte) {
	t.Helper()
	var name string
	var data []byte
	for _, line := range strings.Split(string(payload), "\n") {
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = []byte(strings.TrimPrefix(line, "data: "))
		}
	}
	return name, data
}

func asPublicError(err error, target **publicError) bool {
	public, ok := err.(*publicError)
	if ok {
		*target = public
	}
	return ok
}
