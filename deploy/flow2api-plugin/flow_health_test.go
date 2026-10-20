package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

const flowTestHealthProject = "77777777-8888-4999-8aaa-bbbbbbbbbbbb"

func flowUnusualRefusal(t *testing.T, rpcID string) string {
	t.Helper()
	return flowErrorEnvelope(t, []any{"wrb.fr", rpcID, nil, nil, nil, []any{3, nil, []any{[]any{"type.googleapis.com/google.rpc.ErrorInfo", []any{"PUBLIC_ERROR_UNUSUAL_ACTIVITY"}}}}, "generic"})
}

// flowCaptureChecks records the health checks a service schedules instead of
// starting timers.
func flowCaptureChecks(service *service) (*[]time.Duration, *[]func()) {
	delays, checks := &[]time.Duration{}, &[]func(){}
	service.afterFunc = func(delay time.Duration, check func()) *time.Timer {
		*delays, *checks = append(*delays, delay), append(*checks, check)
		timer := time.NewTimer(time.Hour)
		timer.Stop()
		return timer
	}
	return delays, checks
}

func TestFlowHealthCheckEndsTheHoldFromItsOwnProject(t *testing.T) {
	// Given: Flow refusing a video for unusual activity and accepting the
	// health check's image, with the health check project already listed.
	fixture := newFlowFixture(t)
	media := []any{flowTestOp, flowTestProject, flowTestMedia}
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("UpteDb", rpcEnvelope(t, "UpteDb", []any{[]any{[]any{flowTestProject, []any{"CLIProxyAPI"}}, []any{flowTestHealthProject, []any{flowHealthTitle}}}}))
	refused := flowUnusualRefusal(t, "YhhmEf")
	fixture.reply("YhhmEf", refused, refused, refused, rpcEnvelope(t, "YhhmEf", []any{[]any{media}}))
	fixture.reply("jwpduf", rpcEnvelope(t, "jwpduf", []any{[]any{append(slices.Clone(media), fixture.link("video"))}}))
	fixture.reply("ogiZ0b", rpcEnvelope(t, "ogiZ0b", []any{[]any{[]any{flowTestMedia, fixture.link("image")}}}))
	service, record := flowService(t, fixture)
	delays, checks := flowCaptureChecks(service)
	video := `{"contents":[{"parts":[{"text":"a fox"}]}],"generationConfig":{"durationSeconds":8}}`

	// When: the refusal holds the account, a caller asks again, and the
	// scheduled health check runs.
	first := flowExecute(t, service, record, "flow-veo-3.1-lite", video)
	held := flowExecute(t, service, record, "flow-veo-3.1-lite", video)
	if len(*checks) != 1 || (*delays)[0] != flowUnusualActivityHold {
		t.Fatalf("health checks scheduled after %v", *delays)
	}
	(*checks)[0]()

	// Then: the caller was held without a submission, the check generated in
	// its own project, and the account is sent again.
	if first.OK || first.Error.Code != "flow_captcha_rejected" || held.OK || held.Error.Code != "flow_unusual_activity_hold" || fixture.count("YhhmEf") != flowTokenAttempts {
		t.Fatalf("first %+v, held %+v after %d submissions", first.Error, held.Error, fixture.count("YhhmEf"))
	}
	if fixture.count("ogiZ0b") != 1 || jsonField(fixture.args("ogiZ0b", 0), 3, 5) != flowTestHealthProject {
		t.Fatalf("%d health check submissions, project %v", fixture.count("ogiZ0b"), jsonField(fixture.args("ogiZ0b", 0), 3, 5))
	}
	if mime, _ := flowInlineMedia(t, flowExecute(t, service, record, "flow-veo-3.1-lite", video)); mime != "video/mp4" {
		t.Fatalf("after the health check the video was %q", mime)
	}
}

func TestFlowRefusedHealthCheckDoublesTheConfiguredHold(t *testing.T) {
	// Given: a configured health check project and a one-minute first hold,
	// and Flow refusing the video and then the health check's image.
	fixture := newFlowFixture(t)
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("YhhmEf", flowUnusualRefusal(t, "YhhmEf"))
	fixture.reply("ogiZ0b", flowUnusualRefusal(t, "ogiZ0b"))
	service, record := flowService(t, fixture)
	service.config.HealthCheckProject = flowTestHealthProject
	service.config.UnusualActivityHoldSeconds = 60
	clock := time.Unix(1700000000, 0)
	service.now = func() time.Time { return clock }
	delays, checks := flowCaptureChecks(service)
	video := `{"contents":[{"parts":[{"text":"a fox"}]}],"generationConfig":{"durationSeconds":8}}`

	// When: the hold ends, its health check is refused, and a caller asks.
	flowExecute(t, service, record, "flow-veo-3.1-lite", video)
	clock = clock.Add(time.Minute)
	(*checks)[0]()
	held := flowExecute(t, service, record, "flow-veo-3.1-lite", video)

	// Then: the check went to the configured project without a listing, the
	// next check waits twice as long, and the caller is still held.
	if !slices.Equal(*delays, []time.Duration{time.Minute, 2 * time.Minute}) || fixture.count("UpteDb") != 0 {
		t.Fatalf("checks after %v, %d project listings", *delays, fixture.count("UpteDb"))
	}
	if jsonField(fixture.args("ogiZ0b", 0), 3, 5) != flowTestHealthProject || held.OK || held.Error.Code != "flow_unusual_activity_hold" || fixture.count("YhhmEf") != flowTokenAttempts {
		t.Fatalf("project %v, held %+v after %d submissions", jsonField(fixture.args("ogiZ0b", 0), 3, 5), held.Error, fixture.count("YhhmEf"))
	}
}

type flowTelegramCard struct {
	Path      string `json:"-"`
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

// newFlowTelegramFixture answers as the Telegram bot API and hands each card
// it receives to the test.
func newFlowTelegramFixture(t *testing.T) (*httptest.Server, chan flowTelegramCard) {
	t.Helper()
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	cards := make(chan flowTelegramCard, 8)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var card flowTelegramCard
		if err := json.NewDecoder(request.Body).Decode(&card); err != nil {
			t.Error(err)
		}
		card.Path = request.URL.Path
		cards <- card
		writeFixture(t, writer, `{"ok":true}`)
	}))
	t.Cleanup(server.Close)
	return server, cards
}

func nextFlowCard(t *testing.T, cards chan flowTelegramCard) flowTelegramCard {
	t.Helper()
	select {
	case card := <-cards:
		return card
	case <-time.After(5 * time.Second):
		t.Fatal("no Telegram card arrived")
		return flowTelegramCard{}
	}
}

func TestFlowHealthCardsAnnounceTheBlockAndTheRecovery(t *testing.T) {
	// Given: Telegram configured, Flow refusing a video for unusual activity
	// and accepting the health check's image.
	fixture := newFlowFixture(t)
	telegram, cards := newFlowTelegramFixture(t)
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("YhhmEf", flowUnusualRefusal(t, "YhhmEf"))
	fixture.reply("ogiZ0b", rpcEnvelope(t, "ogiZ0b", []any{[]any{[]any{flowTestMedia, fixture.link("image")}}}))
	service, record := flowService(t, fixture)
	service.config.HealthCheckProject = flowTestHealthProject
	service.config.TelegramBotToken, service.config.TelegramChatID = "123:fixture", "42"
	service.telegramOrigin = telegram.URL
	_, checks := flowCaptureChecks(service)
	video := `{"contents":[{"parts":[{"text":"a fox"}]}],"generationConfig":{"durationSeconds":8}}`

	// When: the refusal holds the account and the hold's health check passes.
	flowExecute(t, service, record, "flow-veo-3.1-lite", video)
	blocked := nextFlowCard(t, cards)
	(*checks)[0]()
	recovered := nextFlowCard(t, cards)

	// Then: one card announced the block and one the recovery, to the
	// configured bot and chat.
	if blocked.Path != "/bot123:fixture/sendMessage" || blocked.ChatID != "42" || blocked.ParseMode != "HTML" ||
		!strings.Contains(blocked.Text, "차단 감지") || !strings.Contains(recovered.Text, "복구") {
		t.Fatalf("cards %+v then %+v", blocked, recovered)
	}
}

func TestFlowPeriodicHealthCheckSkipsAnAccountInUse(t *testing.T) {
	// Given: an account Flow accepts, checked hourly in the health project.
	fixture := newFlowFixture(t)
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("ogiZ0b", rpcEnvelope(t, "ogiZ0b", []any{[]any{[]any{flowTestMedia, fixture.link("image")}}}))
	service, record := flowService(t, fixture)
	service.config.HealthCheckProject = flowTestHealthProject
	clock := time.Unix(1700000000, 0)
	service.now = func() time.Time { return clock }

	// When: the idle account is looked at, again 59 minutes later, after a
	// caller's image at 61 minutes, and an hour after that.
	service.periodicHealthChecks()
	clock = clock.Add(59 * time.Minute)
	service.periodicHealthChecks()
	idle := fixture.count("ogiZ0b")
	clock = clock.Add(2 * time.Minute)
	flowInlineMedia(t, flowExecute(t, service, record, "flow-nano-banana-2", `{"contents":[{"parts":[{"text":"a fox"}]}]}`))
	service.periodicHealthChecks()
	used := fixture.count("ogiZ0b")
	clock = clock.Add(time.Hour)
	service.periodicHealthChecks()

	// Then: the first look checked the idle account in the health project,
	// neither a recent check nor a caller's image let another check run, and
	// an hour without either did.
	if idle != 1 || used != 2 || fixture.count("ogiZ0b") != 3 || jsonField(fixture.args("ogiZ0b", 0), 3, 5) != flowTestHealthProject {
		t.Fatalf("submissions %d after idle looks, %d after the caller, %d in all", idle, used, fixture.count("ogiZ0b"))
	}
}

func TestFlowTelegramTestCardAndHealthListing(t *testing.T) {
	// Given: Telegram configured and an account Flow refused for unusual activity.
	fixture := newFlowFixture(t)
	telegram, cards := newFlowTelegramFixture(t)
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("YhhmEf", flowUnusualRefusal(t, "YhhmEf"))
	service, record := flowService(t, fixture)
	service.config.TelegramBotToken, service.config.TelegramChatID = "123:fixture", "42"
	service.telegramOrigin = telegram.URL
	flowCaptureChecks(service)
	flowExecute(t, service, record, "flow-veo-3.1-lite", `{"contents":[{"parts":[{"text":"a fox"}]}],"generationConfig":{"durationSeconds":8}}`)
	nextFlowCard(t, cards)

	// When: a test card is requested and the accounts are listed.
	sent := invoke(t, service, "management.handle", managementRequest{Method: "POST", Path: flowTelegramTestPath})
	card := nextFlowCard(t, cards)
	result, err := service.flowStatus(context.Background(), managementRequest{})
	if err != nil {
		t.Fatal(err)
	}

	// Then: the test card reached the chat, and the listing shows the hold,
	// its check time, and that alerts are configured.
	var listing struct {
		Accounts []flowAccountView `json:"accounts"`
		Checks   struct {
			Interval int64 `json:"interval_seconds"`
			Telegram bool  `json:"telegram"`
		} `json:"health_checks"`
	}
	if err := json.Unmarshal(jsonFixture(t, result), &listing); err != nil {
		t.Fatal(err)
	}
	health := listing.Accounts[0].Health
	if !sent.OK || !strings.Contains(card.Text, "알림 테스트") || health == nil || health.Status != flowHealthBlocked || health.HeldUntil == 0 || health.NextCheck != health.HeldUntil ||
		!listing.Checks.Telegram || listing.Checks.Interval != int64(flowHealthEvery/time.Second) {
		t.Fatalf("test card %+v (%+v), listing %+v %+v", card, sent.Error, health, listing.Checks)
	}

	// When: Telegram is not configured, the test card is refused by name.
	service.config.TelegramBotToken, service.config.TelegramChatID = "", ""
	unconfigured := invoke(t, service, "management.handle", managementRequest{Method: "POST", Path: flowTelegramTestPath})
	var refusal httpResponse
	if !unconfigured.OK || json.Unmarshal(unconfigured.Result, &refusal) != nil || refusal.StatusCode != 409 || string(refusal.Body) != `{"error":"flow_telegram_unconfigured"}` {
		t.Fatalf("unconfigured test card answered %+v %d %s", unconfigured.Error, refusal.StatusCode, refusal.Body)
	}
}

func TestFlowInconclusiveHealthCheckKeepsTheHold(t *testing.T) {
	// Given: a held account whose checks cannot tell: first the project lookup
	// fails, then the health model's daily quota is spent.
	fixture := newFlowFixture(t)
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("YhhmEf", flowUnusualRefusal(t, "YhhmEf"))
	fixture.reply("UpteDb", rpcEnvelope(t, "UpteDb", "not a project list"))
	service, record := flowService(t, fixture)
	delays, checks := flowCaptureChecks(service)
	video := `{"contents":[{"parts":[{"text":"a fox"}]}],"generationConfig":{"durationSeconds":8}}`
	flowExecute(t, service, record, "flow-veo-3.1-lite", video)

	// When: both checks run.
	(*checks)[0]()
	service.config.HealthCheckProject = flowTestHealthProject
	service.holdDailyQuota(record.SourceAuthID, flowHealthModel, &publicError{Code: "flow_quota_exhausted", Message: "flow_quota_exhausted: PUBLIC_ERROR_PER_MODEL_DAILY_QUOTA_REACHED"})
	(*checks)[1]()
	held := flowExecute(t, service, record, "flow-veo-3.1-lite", video)

	// Then: each kept the hold and scheduled the next check after the retry
	// wait, nothing reached Flow, and the caller is still held.
	_, stillHeld := service.unusualActivityHeld(record.SourceAuthID)
	if !slices.Equal(*delays, []time.Duration{flowUnusualActivityHold, flowHealthRetry, flowHealthRetry}) || fixture.count("ogiZ0b") != 0 || !stillHeld ||
		held.OK || held.Error.Code != "flow_unusual_activity_hold" || fixture.count("YhhmEf") != flowTokenAttempts {
		t.Fatalf("checks after %v, %d images, held %v, caller %+v", *delays, fixture.count("ogiZ0b"), stillHeld, held.Error)
	}
	// Then: the account still shows blocked, not failing, with the failed check.
	if health := service.healthView(record.SourceAuthID); health.Status != flowHealthBlocked || health.CheckKind != "hold" || health.CheckResult != "failed" {
		t.Fatalf("health after the checks %+v", health)
	}
}

func TestFlowLateRefusalLeavesThePendingCheckAlone(t *testing.T) {
	// Given: a held account whose check is scheduled, and a request admitted
	// before the hold that Flow refuses late, as when several were waiting.
	fixture := newFlowFixture(t)
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("YhhmEf", flowUnusualRefusal(t, "YhhmEf"))
	fixture.reply("ogiZ0b", rpcEnvelope(t, "ogiZ0b", []any{[]any{[]any{flowTestMedia, fixture.link("image")}}}))
	service, record := flowService(t, fixture)
	service.config.HealthCheckProject = flowTestHealthProject
	delays, checks := flowCaptureChecks(service)
	flowExecute(t, service, record, "flow-veo-3.1-lite", `{"contents":[{"parts":[{"text":"a fox"}]}],"generationConfig":{"durationSeconds":8}}`)

	// When: the late refusal arrives, then the check runs.
	late := service.noteUnusualActivity(record, &publicError{Code: "flow_captcha_rejected", Message: "flow_captcha_rejected: PUBLIC_ERROR_UNUSUAL_ACTIVITY"}, nil)
	(*checks)[0]()

	// Then: the late refusal neither doubled the hold nor replaced its check,
	// and the check's accepted image ended the hold.
	if _, held := service.unusualActivityHeld(record.SourceAuthID); late != 0 || !slices.Equal(*delays, []time.Duration{flowUnusualActivityHold}) || held || fixture.count("ogiZ0b") != 1 {
		t.Fatalf("late refusal held %v, checks after %v, held %v after %d images", late, *delays, held, fixture.count("ogiZ0b"))
	}
}

func TestFlowCheckReplacedWhileRunningSchedulesNoRetry(t *testing.T) {
	// Given: a held account whose check cannot tell, and whose schedule is
	// replaced while that check waits for Flow, as when the host keeps a
	// quiesced instance and registers it again.
	fixture := newFlowFixture(t)
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("YhhmEf", flowUnusualRefusal(t, "YhhmEf"))
	fixture.reply("UpteDb", rpcEnvelope(t, "UpteDb", "not a project list"))
	service, record := flowService(t, fixture)
	clock := time.Unix(1700000000, 0)
	service.now = func() time.Time { return clock }
	delays, checks := flowCaptureChecks(service)
	flowExecute(t, service, record, "flow-veo-3.1-lite", `{"contents":[{"parts":[{"text":"a fox"}]}],"generationConfig":{"durationSeconds":8}}`)
	fixture.onRPC = func(rpcID string) {
		if rpcID == "UpteDb" {
			service.quiesce()
			service.resumeQuiesced()
		}
	}

	// When: the check runs and finishes without an answer.
	(*checks)[0]()

	// Then: the replacement check stands, and the finished check scheduled no
	// retry over it.
	if _, held := service.unusualActivityHeld(record.SourceAuthID); !held || !slices.Equal(*delays, []time.Duration{flowUnusualActivityHold, flowHealthWindow}) {
		t.Fatalf("held %v, checks after %v", held, *delays)
	}
}

func TestFlowStaleAcceptanceLeavesTheHold(t *testing.T) {
	// Given: a held account, and a request admitted before the hold whose
	// accepted answer arrives after it, and a check whose schedule was
	// replaced before its accepted image came back.
	fixture := newFlowFixture(t)
	telegram, cards := newFlowTelegramFixture(t)
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("YhhmEf", flowUnusualRefusal(t, "YhhmEf"))
	fixture.reply("ogiZ0b", rpcEnvelope(t, "ogiZ0b", []any{[]any{[]any{flowTestMedia, fixture.link("image")}}}))
	service, record := flowService(t, fixture)
	service.config.HealthCheckProject = flowTestHealthProject
	service.config.TelegramBotToken, service.config.TelegramChatID = "123:fixture", "42"
	service.telegramOrigin = telegram.URL
	clock := time.Unix(1700000000, 0)
	service.now = func() time.Time { return clock }
	_, checks := flowCaptureChecks(service)
	flowExecute(t, service, record, "flow-veo-3.1-lite", `{"contents":[{"parts":[{"text":"a fox"}]}],"generationConfig":{"durationSeconds":8}}`)
	nextFlowCard(t, cards)

	// When: the earlier request's acceptance arrives, then the check runs and
	// its schedule is replaced while its image is being generated.
	service.noteUnusualActivity(record, nil, nil)
	_, heldAfterLate := service.unusualActivityHeld(record.SourceAuthID)
	fixture.onRPC = func(rpcID string) {
		if rpcID == "ogiZ0b" {
			service.quiesce()
			service.resumeQuiesced()
		}
	}
	(*checks)[0]()
	_, heldAfterReplaced := service.unusualActivityHeld(record.SourceAuthID)
	fixture.onRPC = nil
	(*checks)[1]()

	// Then: neither stale acceptance ended the hold or marked the account
	// healthy; the replacement check's accepted image did, with one recovery
	// card.
	if !heldAfterLate || !heldAfterReplaced || fixture.count("ogiZ0b") != 2 {
		t.Fatalf("held after the late acceptance %v, after the replaced check %v, %d images", heldAfterLate, heldAfterReplaced, fixture.count("ogiZ0b"))
	}
	if _, held := service.unusualActivityHeld(record.SourceAuthID); held || !strings.Contains(nextFlowCard(t, cards).Text, "복구") {
		t.Fatalf("held %v after the replacement check", held)
	}
}

func TestFlowCallbackOfAnEndedHoldDoesNotCheckItsSuccessor(t *testing.T) {
	// Given: a hold that ended through its accepted check, and a new hold
	// whose first check carries the same generation number.
	fixture := newFlowFixture(t)
	refused := flowUnusualRefusal(t, "YhhmEf")
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("YhhmEf", refused, refused, refused, refused, refused, refused)
	fixture.reply("ogiZ0b", rpcEnvelope(t, "ogiZ0b", []any{[]any{[]any{flowTestMedia, fixture.link("image")}}}))
	service, record := flowService(t, fixture)
	service.config.HealthCheckProject = flowTestHealthProject
	_, checks := flowCaptureChecks(service)
	video := `{"contents":[{"parts":[{"text":"a fox"}]}],"generationConfig":{"durationSeconds":8}}`
	flowExecute(t, service, record, "flow-veo-3.1-lite", video)
	(*checks)[0]()
	flowExecute(t, service, record, "flow-veo-3.1-lite", video)

	// When: the ended hold's callback runs again.
	(*checks)[0]()

	// Then: it sent nothing for the new hold, which waits for its own check.
	if _, held := service.unusualActivityHeld(record.SourceAuthID); !held || fixture.count("ogiZ0b") != 1 || len(*checks) != 2 {
		t.Fatalf("held %v after %d images and %d scheduled checks", held, fixture.count("ogiZ0b"), len(*checks))
	}
}

func TestFlowQuiescedInstanceLeavesItsChecksAloneUntilRegistered(t *testing.T) {
	// Given: a held account whose check is scheduled, Telegram configured, and
	// Flow accepting the health check's image.
	fixture := newFlowFixture(t)
	telegram, cards := newFlowTelegramFixture(t)
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("YhhmEf", flowUnusualRefusal(t, "YhhmEf"))
	fixture.reply("ogiZ0b", rpcEnvelope(t, "ogiZ0b", []any{[]any{[]any{flowTestMedia, fixture.link("image")}}}))
	service, record := flowService(t, fixture)
	service.config.HealthCheckProject = flowTestHealthProject
	service.config.TelegramBotToken, service.config.TelegramChatID = "123:fixture", "42"
	service.telegramOrigin = telegram.URL
	clock := time.Unix(1700000000, 0)
	service.now = func() time.Time { return clock }
	delays, checks := flowCaptureChecks(service)
	flowExecute(t, service, record, "flow-veo-3.1-lite", `{"contents":[{"parts":[{"text":"a fox"}]}],"generationConfig":{"durationSeconds":8}}`)
	nextFlowCard(t, cards)

	// When: a hot reload quiesces the instance, the hold's time and an hour
	// pass, and the stopped timer fires late.
	quiesced := invoke(t, service, "plugin.quiesce", nil)
	clock = clock.Add(flowUnusualActivityHold + flowHealthEvery)
	(*checks)[0]()
	service.periodicHealthChecks()

	// Then: neither the held account's check nor a periodic check reached
	// Flow, and the account stays held.
	if _, held := service.unusualActivityHeld(record.SourceAuthID); !quiesced.OK || fixture.count("ogiZ0b") != 0 || !held {
		t.Fatalf("quiesce %+v, %d images, held %v", quiesced.Error, fixture.count("ogiZ0b"), held)
	}

	// When: the host keeps the instance, as when its replacement failed to
	// load, and the check it re-arms runs.
	service.resumeQuiesced()
	(*checks)[0]()
	stale := fixture.count("ogiZ0b")
	(*checks)[1]()

	// Then: the replaced timer's late call changed nothing, the check was due
	// at once, and its accepted image ended the hold with a recovery card.
	if _, held := service.unusualActivityHeld(record.SourceAuthID); stale != 0 || held || fixture.count("ogiZ0b") != 1 || (*delays)[1] != 0 || !strings.Contains(nextFlowCard(t, cards).Text, "복구") {
		t.Fatalf("held %v after %d images, checks after %v", held, fixture.count("ogiZ0b"), *delays)
	}
}

func TestFlowRegisterResumesAQuiescedInstance(t *testing.T) {
	service := newService(nil)
	t.Cleanup(service.stop)
	if reply := invoke(t, service, "plugin.quiesce", nil); !reply.OK {
		t.Fatalf("quiesce: %+v", reply.Error)
	}
	if registered := invoke(t, service, "plugin.register", nil); !registered.OK {
		t.Fatalf("register: %+v", registered.Error)
	}
	service.flowMu.Lock()
	quiesced := service.quiesced
	service.flowMu.Unlock()
	if quiesced {
		t.Fatal("a register left the instance quiesced")
	}
}

func TestFlowRunningCheckNamesNoPastTime(t *testing.T) {
	// Given: a held account whose check is running past the time it was due.
	fixture := newFlowFixture(t)
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("YhhmEf", flowUnusualRefusal(t, "YhhmEf"))
	service, record := flowService(t, fixture)
	clock := time.Unix(1700000000, 0)
	service.now = func() time.Time { return clock }
	flowCaptureChecks(service)
	video := `{"contents":[{"parts":[{"text":"a fox"}]}],"generationConfig":{"durationSeconds":8}}`
	flowExecute(t, service, record, "flow-veo-3.1-lite", video)
	clock = clock.Add(flowUnusualActivityHold)

	// When: a caller asks.
	held := flowExecute(t, service, record, "flow-veo-3.1-lite", video)

	// Then: it is held, told a check is running rather than a time already past.
	if held.OK || held.Error.Code != "flow_unusual_activity_hold" || !strings.Contains(held.Error.Message, "health check of the account is running") {
		t.Fatalf("caller answered %+v", held.Error)
	}
}

func TestFlowPeriodicCheckOnASpentQuotaIsNotAFailure(t *testing.T) {
	// Given: an idle account whose health model's daily quota is spent.
	fixture := newFlowFixture(t)
	service, record := flowService(t, fixture)
	service.config.HealthCheckProject = flowTestHealthProject
	service.holdDailyQuota(record.SourceAuthID, flowHealthModel, &publicError{Code: "flow_quota_exhausted", Message: "flow_quota_exhausted: PUBLIC_ERROR_PER_MODEL_DAILY_QUOTA_REACHED"})

	// When: the periodic check runs.
	service.periodicHealthChecks()

	// Then: the failed check is recorded, nothing reached Flow, and the
	// account is not marked failing, which would have sent a card.
	health := service.healthView(record.SourceAuthID)
	if health.Status == flowHealthFailing || health.CheckKind != "periodic" || health.CheckResult != "failed" || fixture.count("ogiZ0b") != 0 {
		t.Fatalf("health after a check on a spent quota %+v", health)
	}
}
