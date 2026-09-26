package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewRequestsRoundRobinAfterFilteringExhaustedAccounts(t *testing.T) {
	service, local := continuationFixture(t)
	ids := []string{local.Target.ID}
	for _, seed := range []string{"b", "c", "d"} {
		record := recordFixture(t, seed)
		seedSession(t, service, record, sessionToken{encodedToken("seed-" + seed)})
		ids = append(ids, record.ID)
	}
	full, used := 1.0, 0.9
	reset := float64(service.now().Add(time.Hour).Unix())
	for _, id := range ids[:2] {
		service.observeQuota(id, &usageView{Metrics: []usageMetric{{WindowKind: "5h", UsageFraction: &full, ResetUnixSeconds: &reset}}})
	}
	service.observeQuota(ids[2], &usageView{Metrics: []usageMetric{{WindowKind: "5h", UsageFraction: &used}}})
	counts := map[string]int{}
	for range 8 {
		pick := service.pickServableAccount(interactionOmniModel, slotCandidates(ids...))
		counts[pick.AuthID]++
	}
	if counts[ids[2]] != 4 || counts[ids[3]] != 4 || len(counts) != 2 {
		t.Fatalf("uneven rotation among eligible accounts: %+v", counts)
	}
}

func TestWeeklyExhaustionExcludesAccountUntilItsReset(t *testing.T) {
	service, local := continuationFixture(t)
	other := recordFixture(t, "b")
	seedSession(t, service, other, sessionToken{encodedToken("other")})
	now := service.now()
	service.now = func() time.Time { return now }
	full := 1.0
	reset := float64(now.Add(7 * time.Hour).Unix())
	service.observeQuota(local.Target.ID, &usageView{Metrics: []usageMetric{{WindowKind: "weekly", UsageFraction: &full, ResetUnixSeconds: &reset}}})
	now = now.Add(20 * time.Minute)
	for range 4 {
		pick := service.pickServableAccount(interactionOmniModel, slotCandidates(local.Target.ID, other.ID))
		if pick.AuthID != other.ID {
			t.Fatalf("exhausted weekly quota was selected: %+v", pick)
		}
	}
	now = time.Unix(int64(reset), 0)
	pick := service.pickServableAccount(interactionOmniModel, slotCandidates(local.Target.ID))
	if !pick.Handled || pick.AuthID != local.Target.ID {
		t.Fatalf("account was not eligible after weekly reset: %+v", pick)
	}
}

// The product told this account it had no video left while its five hour
// window read 96%, so rotation kept handing it new turns, each spending twenty
// seconds to be told the same thing, until the window turned over.
func TestAccountTheProductCalledOutOfVideoSitsOutUntilItsWindowTurnsOver(t *testing.T) {
	service, local := continuationFixture(t)
	other := recordFixture(t, "b")
	seedSession(t, service, other, sessionToken{encodedToken("other")})
	now := service.now()
	service.now = func() time.Time { return now }
	fiveHour, weekly := 0.96, 0.46
	fiveHourReset, weeklyReset := float64(now.Add(2*time.Hour).Unix()), float64(now.Add(72*time.Hour).Unix())
	observed := &usageView{Metrics: []usageMetric{
		{WindowKind: "5h", UsageFraction: &fiveHour, ResetUnixSeconds: &fiveHourReset},
		{WindowKind: "weekly", UsageFraction: &weekly, ResetUnixSeconds: &weeklyReset},
	}}
	service.observeQuota(local.Target.ID, observed)
	if !service.quotaAvailable(local.Target.ID) {
		t.Fatal("an account reading 96% was excluded before the product said anything")
	}

	service.noteVideoRefusal(local.Target.ID, webNoVideo("한도가 재설정되는 대로 동영상을 더 생성할 수 있습니다. 설정에서 사용량을 확인해 보세요."))
	// The listing observes the same windows again a minute later.
	service.observeQuota(local.Target.ID, observed)

	for range 4 {
		if pick := service.pickServableAccount(interactionOmniModel, slotCandidates(local.Target.ID, other.ID)); pick.AuthID != other.ID {
			t.Fatalf("an account out of video was handed a new turn: %+v", pick)
		}
	}
	now = time.Unix(int64(fiveHourReset), 0)
	if !service.quotaAvailable(local.Target.ID) {
		t.Fatal("the account stayed out after its five hour window turned over")
	}
}

// One account read 37% of its five hours and 46% of its week when the product
// said it had no video left. Holding it until the weekly reset benched it for
// four days although its five hour window turned over within the hour.
func TestVideoLimitHoldsOnlyUntilTheFiveHourWindowTurnsOver(t *testing.T) {
	service, local := continuationFixture(t)
	now := service.now()
	service.now = func() time.Time { return now }
	fiveHour, weekly := 0.37, 0.46
	fiveHourReset, weeklyReset := float64(now.Add(40*time.Minute).Unix()), float64(now.Add(96*time.Hour).Unix())
	service.observeQuota(local.Target.ID, &usageView{Metrics: []usageMetric{
		{WindowKind: "5h", UsageFraction: &fiveHour, ResetUnixSeconds: &fiveHourReset},
		{WindowKind: "weekly", UsageFraction: &weekly, ResetUnixSeconds: &weeklyReset},
	}})

	service.noteVideoRefusal(local.Target.ID, webNoVideo("죄송하지만, 오늘은 더 이상 영상을 생성해 드릴 수 없습니다. 내일 다시 오시면 더 만들어 드릴 수 있어요."))

	now = time.Unix(int64(fiveHourReset), 0).Add(-time.Minute)
	if service.quotaAvailable(local.Target.ID) {
		t.Fatal("released before the five hour window turned over")
	}
	now = time.Unix(int64(fiveHourReset), 0)
	if !service.quotaAvailable(local.Target.ID) {
		t.Fatal("held on the weekly window after the five hour window turned over")
	}
}

// Declined prompts and a model that answers as text say nothing about the
// account's allowance; holding it for them would starve the fleet.
func TestDeclinedTurnsDoNotHoldTheirAccount(t *testing.T) {
	service, local := continuationFixture(t)
	for _, err := range []error{
		webNoVideo("실제 인물이 그런 상황에 있는 동영상은 만들 수 없습니다. 다른 것으로 도와드릴까요?"),
		webNoVideo("저는 언어 모델일 뿐이라서 그것을 도와드릴 수가 없습니다."),
		failure(502, "web_response_failed"),
	} {
		service.noteVideoRefusal(local.Target.ID, err)
	}
	if !service.quotaAvailable(local.Target.ID) {
		t.Fatal("a declined turn took the account out of rotation")
	}
}

func TestVideoLimitWithoutAnObservedWindowHoldsForAFixedTime(t *testing.T) {
	service, local := continuationFixture(t)
	now := service.now()
	service.now = func() time.Time { return now }
	service.noteVideoRefusal(local.Target.ID, webNoVideo("죄송하지만, 오늘은 더 이상 영상을 생성해 드릴 수 없습니다. 내일 다시 오시면 더 만들어 드릴 수 있어요."))
	now = now.Add(videoLimitHold - time.Minute)
	if service.quotaAvailable(local.Target.ID) {
		t.Fatal("released before the hold passed")
	}
	now = now.Add(time.Minute)
	if !service.quotaAvailable(local.Target.ID) {
		t.Fatal("held past the hold")
	}
}

// The executor is where the product's answer arrives, so the limit has to
// reach the scheduler from a real turn, not only from a direct call.
func TestLimitAnswerToATurnTakesItsAccountOutOfRotation(t *testing.T) {
	service, local := continuationFixture(t)
	continuationWeb(t, service, &continuationWebFixture{answer: "오늘은 더 이상 영상을 생성해 드릴 수 없지만, 웹에서 영상을 찾아드릴 수는 있어요."})
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call

	result := interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`)

	if result.OK || result.Error.Code != "gemini_web_omni:no_video_generated" {
		t.Fatalf("turn: %+v", result.Error)
	}
	if service.quotaAvailable(local.Target.ID) {
		t.Fatal("the account the product called out of video stayed in rotation")
	}
}

func TestExhaustedFiveHourObservationDoesNotExpireBeforeReset(t *testing.T) {
	service, local := continuationFixture(t)
	now := service.now()
	service.now = func() time.Time { return now }
	full := 1.0
	reset := float64(now.Add(5 * time.Hour).Unix())
	service.observeQuota(local.Target.ID, &usageView{Metrics: []usageMetric{{WindowKind: "5h", UsageFraction: &full, ResetUnixSeconds: &reset}}})
	now = now.Add(20 * time.Minute)
	pick, err := service.pickContinuation(jsonFixture(t, map[string]any{
		"Provider": provider, "Model": interactionOmniModel,
		"Candidates": slotCandidates(local.Target.ID),
	}))
	if err == nil || pick.Handled || pick.AuthID != "" {
		t.Fatalf("exhausted fleet delegated back to host: pick=%+v err=%v", pick, err)
	}
}

// qwer941a answered 63 video turns in a row as a text model or with an empty
// reply while both of its windows read almost unused, and a hold on such runs
// kept it out of new rooms. Only an exhausted window or a limit the product
// names takes an account out of the rotation.
func TestAnswersWithoutAVideoKeepTheAccountInNewRooms(t *testing.T) {
	service, local := continuationFixture(t)
	other := recordFixture(t, "b")
	seedSession(t, service, other, sessionToken{encodedToken("other")})
	service.observeQuota(local.Target.ID, roomUsage(47725))
	service.observeQuota(other.ID, roomUsage(30210))

	for range 6 {
		service.noteVideoRefusal(local.Target.ID, webNoVideo("저는 단지 언어 모델일 뿐이고, 그것을 이해하고 응답하는 능력이 없기 때문에 도와드릴 수가 없습니다."))
		service.noteVideoRefusal(local.Target.ID, webNoVideo(""))
	}

	chosen := map[string]int{}
	for range 4 {
		chosen[service.pickServableAccount(interactionOmniModel, slotCandidates(local.Target.ID, other.ID)).AuthID]++
	}
	if chosen[local.Target.ID] != 2 || chosen[other.ID] != 2 {
		t.Fatalf("answers without a video took the account out of new rooms: %+v", chosen)
	}
}

// The executor is where those answers arrive, so real turns must not bench the
// account either.
func TestEmptyRepliesToTurnsKeepTheirAccountInNewRooms(t *testing.T) {
	service, local := continuationFixture(t)
	continuationWeb(t, service, &continuationWebFixture{replyOnly: true})
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call

	for _, input := range []string{"first", "second", "third"} {
		result := interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"`+input+`"}`)
		if result.OK || result.Error.Code != "gemini_web_omni:no_video_generated" {
			t.Fatalf("turn: %+v", result.Error)
		}
	}
	other := recordFixture(t, "b")
	seedSession(t, service, other, sessionToken{encodedToken("other")})

	chosen := map[string]int{}
	for range 4 {
		chosen[service.pickServableAccount(interactionOmniModel, slotCandidates(local.Target.ID, other.ID)).AuthID]++
	}
	if chosen[local.Target.ID] != 2 || chosen[other.ID] != 2 {
		t.Fatalf("replies without a video took the account out of new rooms: %+v", chosen)
	}
}

// videoCapBudget is a CheckGxuBudget body carrying the web app's "You're out of
// videos for now" entry: action 5, state 3, and when videos come back.
func videoCapBudget(until float64) []any {
	return []any{false, []any{slots(6, map[int]any{0: float64(5), 4: []any{until, float64(0)}, 5: float64(3)})}}
}

func windowsUsage(fiveHour, week float64) *usageView {
	return &usageView{Metrics: []usageMetric{
		{RemainingUnits: &fiveHour, WindowKind: "5h", Unit: "provider_compute_unit"},
		{RemainingUnits: &week, WindowKind: "weekly", Unit: "provider_compute_unit"},
	}}
}

func TestWebVideoCapReadsTheNoticeTheWebAppShows(t *testing.T) {
	for _, test := range []struct {
		name   string
		body   any
		capped bool
		until  float64
		ok     bool
	}{
		{"out of videos", videoCapBudget(1790400000), true, 1790400000, true},
		{"another budget spent", []any{true, []any{slots(6, map[int]any{0: float64(1), 5: float64(3)})}}, false, 0, true},
		{"videos not spent", []any{false, []any{slots(6, map[int]any{0: float64(5), 5: float64(2)})}}, false, 0, true},
		{"nothing spent", []any{false}, false, 0, true},
		{"not a budget", "denied", false, 0, false},
	} {
		capped, until, ok := webVideoCap(test.body)
		if capped != test.capped || ok != test.ok || (until != nil) != (test.until != 0) || until != nil && *until != test.until {
			t.Errorf("%s: capped=%v until=%v ok=%v", test.name, capped, until, ok)
		}
	}
}

// qwer941a read 96% of its week left while the product had cut it off from
// video, and it answered every video turn as a text model until the cap lifted.
func TestTheVideoBudgetKeepsAnAccountOutUntilVideosAreBack(t *testing.T) {
	service, local := continuationFixture(t)
	now := service.now()
	service.now = func() time.Time { return now }
	back := float64(now.Add(3 * time.Hour).Unix())
	capped, open := true, false

	service.observeQuota(local.Target.ID, &usageView{VideoCapped: &capped, VideoAvailableAt: &back})
	if service.quotaAvailable(local.Target.ID) {
		t.Fatal("an account out of videos stayed in rotation")
	}
	service.observeQuota(local.Target.ID, windowsUsage(47725, 928701))
	if service.quotaAvailable(local.Target.ID) {
		t.Fatal("a reading without the budget released the cap")
	}
	now = time.Unix(int64(back), 0)
	if !service.quotaAvailable(local.Target.ID) {
		t.Fatal("the account stayed out after videos came back")
	}

	now = now.Add(-time.Hour)
	service.observeQuota(local.Target.ID, &usageView{VideoCapped: &capped, VideoAvailableAt: &back})
	service.observeQuota(local.Target.ID, &usageView{VideoCapped: &open})
	if !service.quotaAvailable(local.Target.ID) {
		t.Fatal("the budget reopening video did not release the account")
	}
}

// A video cost 3,500 to 5,200 units on 2026-09-26, and a room is three of them
// pinned to the account that opens it.
func TestARoomNeedsThreeMeasuredVideosInBothWindows(t *testing.T) {
	service, local := continuationFixture(t)
	other := recordFixture(t, "b")
	seedSession(t, service, other, sessionToken{encodedToken("other")})
	service.observeQuota(local.Target.ID, windowsUsage(48000, 900000))
	service.noteVideoDelivered(local.Target.ID)
	service.noteVideoDelivered(local.Target.ID)
	service.observeQuota(local.Target.ID, windowsUsage(37600, 889600))

	if units := service.generationUnits(); units != 5200 {
		t.Fatalf("a video costs %v, want the 5,200 measured", units)
	}
	for _, test := range []struct {
		name           string
		fiveHour, week float64
		affords        bool
	}{
		{"both windows hold three", 15600, 15600, true},
		{"five hours short", 15599, 900000, false},
		{"week short", 48000, 15599, false},
	} {
		service.observeQuota(other.ID, windowsUsage(test.fiveHour, test.week))
		if service.affordsARoom(other.ID) != test.affords {
			t.Errorf("%s: affords a room = %v", test.name, !test.affords)
		}
	}
}

// Nothing reads the windows while no room opens, so a short reading from a
// window that has since turned over must not keep the account out.
func TestAShortReadingFromAWindowThatTurnedOverKeepsNoAccountOut(t *testing.T) {
	service, local := continuationFixture(t)
	now := service.now()
	service.now = func() time.Time { return now }
	short, full := 10000.0, 900000.0
	later := float64(now.Add(time.Hour).Unix())
	muchLater := float64(now.Add(5 * time.Hour).Unix())
	reading := func(fiveHour, week, fiveHourReset, weekReset float64) *usageView {
		return &usageView{Metrics: []usageMetric{
			{RemainingUnits: &fiveHour, WindowKind: "5h", ResetUnixSeconds: &fiveHourReset},
			{RemainingUnits: &week, WindowKind: "weekly", ResetUnixSeconds: &weekReset},
		}}
	}

	service.observeQuota(local.Target.ID, reading(short, full, later, muchLater))
	if service.affordsARoom(local.Target.ID) {
		t.Fatal("an account short of a room in its five hours opened one")
	}
	now = time.Unix(int64(later), 0)
	if !service.affordsARoom(local.Target.ID) {
		t.Fatal("a five hour window that turned over still kept the account out")
	}

	service.observeQuota(local.Target.ID, reading(full, short, muchLater, later+3600))
	if service.affordsARoom(local.Target.ID) {
		t.Fatal("an account short of a room in its week opened one")
	}
	now = time.Unix(int64(later+3600), 0)
	if !service.affordsARoom(local.Target.ID) {
		t.Fatal("a weekly window that turned over still kept the account out")
	}
}

// A window that turned over between two readings hides what was spent in it.
func TestAWindowThatTurnedOverPricesNoVideo(t *testing.T) {
	service, local := continuationFixture(t)
	before, after := 46000.0, 44000.0
	first, second := float64(1790400000), float64(1790418000)
	service.observeQuota(local.Target.ID, &usageView{Metrics: []usageMetric{{RemainingUnits: &before, WindowKind: "5h", ResetUnixSeconds: &first}}})
	service.noteVideoDelivered(local.Target.ID)
	service.observeQuota(local.Target.ID, &usageView{Metrics: []usageMetric{{RemainingUnits: &after, WindowKind: "5h", ResetUnixSeconds: &second}}})

	if units := service.generationUnits(); units != defaultGenerationUnits {
		t.Fatalf("a video was priced across a window turnover: %v", units)
	}
}

// The product cut the account off from video and the turn came back as a text
// answer; the next room has to know that without spending a turn to learn it.
func TestATurnWithoutAVideoReadsTheVideoBudgetOnItsSession(t *testing.T) {
	service, local := continuationFixture(t)
	back := float64(service.now().Add(2 * time.Hour).Unix())
	continuationWeb(t, service, &continuationWebFixture{answer: "저는 단지 언어 모델일 뿐이고, 그것을 이해하고 응답하는 능력이 없기 때문에 도와드릴 수가 없습니다.", budget: videoCapBudget(back)})
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call

	result := interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`)

	if result.OK || result.Error.Code != "gemini_web_omni:no_video_generated" {
		t.Fatalf("turn: %+v", result.Error)
	}
	if service.quotaAvailable(local.Target.ID) {
		t.Fatal("the account its own budget called out of video stayed in rotation")
	}
}

// A delivered video is priced from the reading its own turn takes afterwards.
func TestADeliveredVideoIsPricedFromItsTurnsReading(t *testing.T) {
	service, local := continuationFixture(t)
	fiveHour := slots(4, map[int]any{0: float64(43200), 1: 0.1, 2: float64(1)})
	week := slots(4, map[int]any{0: float64(900000), 1: 0.07, 2: float64(2)})
	continuationWeb(t, service, &continuationWebFixture{video: true, usage: []any{float64(3), []any{week, fiveHour}, false}})
	service.host = (&loginHostFixture{records: map[string]json.RawMessage{local.Target.ID: jsonFixture(t, local.Target)}, service: service}).call
	service.observeQuota(local.Target.ID, windowsUsage(48000, 904800))

	interactionID(t, interactionCall(t, service, local, `{"model":"gemini-omni-1.1-flash","input":"first"}`))

	if units := service.generationUnits(); units != 4800 {
		t.Fatalf("a video costs %v, want the 4,800 its turn spent", units)
	}
}
