package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAccountDisplayNameSanitizesTheLabel(t *testing.T) {
	for label, want := range map[string]string{
		"sunmin938a@gmail.com":                   "sunmin938a",
		"QWS941701@Gmail.com":                    "qws941701",
		"  Some Label  ":                         "some-label",
		"a b/c@example.com":                      "a-b-c",
		"first.last_x-1@host.org":                "first.last_x-1",
		".hidden@example.com":                    "hidden",
		"@example.com":                           "example.com",
		"plain":                                  "plain",
		"\u00ed\u0095\u009c":                     "",
		"":                                       "",
		strings.Repeat("a", 40):                  strings.Repeat("a", 32),
		strings.Repeat("b", 40) + "@example.com": strings.Repeat("b", 32),
	} {
		got := accountDisplayName(label)
		if got != want {
			t.Errorf("accountDisplayName(%q) = %q, want %q", label, got, want)
		}
		if got != "" && !readableAccountPattern.MatchString(got) {
			t.Errorf("accountDisplayName(%q) = %q does not match the contract", label, got)
		}
	}
}

func TestAccountUsageRoundsToOneDecimalAndOmitsUnseenWindows(t *testing.T) {
	service := newService(nil)
	service.quota.entries = map[string]quotaSnapshot{
		"both":      {roomUsed: 0.4567, roomUsedSeen: true, weekUsed: 0.0004, weekUsedSeen: true},
		"roomOnly":  {roomUsed: 0.25, roomUsedSeen: true, weekUsed: 0.9, weekUsedSeen: false},
		"weekOnly":  {roomUsed: 0.9, roomUsedSeen: false, weekUsed: 1.0, weekUsedSeen: true},
		"over":      {roomUsed: 1.7, roomUsedSeen: true},
		"neverSeen": {roomUsed: 0.5, weekUsed: 0.5},
	}
	if got := service.accountUsage("both"); len(got) != 2 || got["5h"] != 45.7 || got["weekly"] != 0 {
		t.Fatalf("both windows = %v, want 5h 45.7 and weekly 0", got)
	}
	if got := service.accountUsage("roomOnly"); len(got) != 1 || got["5h"] != 25 {
		t.Fatalf("room only = %v, want just 5h 25", got)
	}
	if got := service.accountUsage("weekOnly"); len(got) != 1 || got["weekly"] != 100 {
		t.Fatalf("week only = %v, want just weekly 100", got)
	}
	if got := service.accountUsage("over"); got["5h"] != 100 {
		t.Fatalf("a reading above the window is capped at 100, got %v", got)
	}
	for _, id := range []string{"neverSeen", "unknown"} {
		if got := service.accountUsage(id); got != nil {
			t.Fatalf("%s has no observed window, got %v", id, got)
		}
	}
}

func TestAccountDisplayPutsNameAndUsageOnlyWhenKnown(t *testing.T) {
	service := namedAccountService(t)
	service.quota.entries = map[string]quotaSnapshot{"account.json": {roomUsed: 0.123, roomUsedSeen: true}}

	body := map[string]any{}
	service.accountDisplay("account.json").put(body)
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"account":"someone","account_usage":{"5h":12.3}}` {
		t.Fatalf("account fields = %s", encoded)
	}

	service.quota.entries = nil
	body = map[string]any{}
	service.accountDisplay("account.json").put(body)
	if _, present := body["account_usage"]; present || body["account"] != namedAccount {
		t.Fatalf("an account never read must have a name and no usage: %v", body)
	}

	body = map[string]any{}
	accountDisplay{}.put(body)
	if len(body) != 0 {
		t.Fatalf("no account chosen must add nothing: %v", body)
	}
}

func TestRenderedInteractionCarriesAccountUsage(t *testing.T) {
	service := namedAccountService(t)
	service.quota.entries = map[string]quotaSnapshot{"account.json": {weekUsed: 0.5, weekUsedSeen: true}}
	result, err := service.renderInteraction("account.json", continuationResult{}, continuationView{Token: "t", State: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Account string             `json:"account"`
		Usage   map[string]float64 `json:"account_usage"`
	}
	if err := json.Unmarshal(result.(continuationResult).Payload, &body); err != nil {
		t.Fatal(err)
	}
	if body.Account != namedAccount || len(body.Usage) != 1 || body.Usage["weekly"] != 50 {
		t.Fatalf("interaction account fields = %+v", body)
	}
}

func TestExecutorFailureCarriesAccountUsage(t *testing.T) {
	service, local := continuationFixture(t)
	service.quota.entries = map[string]quotaSnapshot{local.Target.ID: {roomUsed: 0.8, roomUsedSeen: true, weekUsed: 0.3, weekUsedSeen: true}}
	auth, err := authFromRecord(local.Target)
	if err != nil {
		t.Fatal(err)
	}
	request := executorRequest{AuthID: local.Target.ID, AuthProvider: provider, Model: interactionOmniModel, Format: "interactions", SourceFormat: "interactions", StorageJSON: auth.StorageJSON, AuthMetadata: auth.Metadata, Payload: []byte(`{"model":"gemini-omni-1.1-flash","input":"x"}`)}
	result := invoke(t, service, "executor.execute", request)
	if result.OK || result.Error == nil {
		t.Fatal("the turn was expected to fail")
	}
	var body struct {
		Error struct {
			Account string             `json:"account"`
			Usage   map[string]float64 `json:"account_usage"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(result.Error.Message), &body); err != nil {
		t.Fatalf("the failure was not the interaction error object: %q", result.Error.Message)
	}
	if body.Error.Account != accountDisplayName(local.Target.Label) || body.Error.Usage["5h"] != 80 || body.Error.Usage["weekly"] != 30 {
		t.Fatalf("error account fields = %+v", body.Error)
	}
}
