package main

import (
	"strings"
	"testing"
)

func diagFixture() *videoTurnDiag {
	account := webAccount{
		Capabilities: []capability{
			{CapabilityID: "0123456789abcdef", DisplayName: "3.8 Flash", Mode: 1},
			{CapabilityID: "fedcba9876543210", DisplayName: "3.8 Pro", Mode: 2},
		},
		CapacityFlags: []int{16, 38},
	}
	diag := newVideoTurnDiag("abcdef0123456789", "first", account, account.Capabilities[0], webFramingPortrait, 1)
	diag.observe([]any{[]any{"rc_x"}, []any{"저는 언어 모델이라서 영상을 만들 수 없습니다. 죄송합니다 정말로 그렇습니다"}, nil, nil, nil, nil, nil, nil, []any{float64(2)}, nil, nil, nil, []any{}})
	return diag
}

func TestVideoDiagFieldsCarryTheTurnWithoutThePrompt(t *testing.T) {
	fields := videoDiagFields(diagFixture(), "no_video")

	rendered := map[string]bool{"provider": true, "state": true, "reason": true, "error": true, "budget": true, "remote_transport": true}
	for name := range fields {
		if !rendered[name] {
			t.Fatalf("field %q is not one the host formatter renders", name)
		}
	}
	if fields["state"] != "video_turn_diag" || fields["error"] != "no_video" {
		t.Fatalf("state/error = %v/%v", fields["state"], fields["error"])
	}
	reason, _ := fields["reason"].(string)
	for _, want := range []string{"account=abcdef ", "kind=first", "capability=3.8 Flash", "mode=1", "id8=01234567", "capacity=1", "thinking=1", "chip=17"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("reason = %q, missing %q", reason, want)
		}
	}
	if budget, _ := fields["budget"].(string); budget != "caps=3.8_Flash/1/01234567,3.8_Pro/2/fedcba98" {
		t.Fatalf("budget = %q", budget)
	}
	response, _ := fields["remote_transport"].(string)
	if !strings.Contains(response, "candidate_shape=0,1,8,12 c8_0=2") {
		t.Fatalf("response = %q, want the candidate layout", response)
	}
	if !strings.Contains(response, "reply_head=저는 언어 모델이라서 영상을 만들 수 없습니다. 죄송합니다 정말로") {
		t.Fatalf("response = %q, want the first 40 runes of the refusal", response)
	}
	if strings.Contains(response, "정말로 그렇습니다") {
		t.Fatalf("response = %q, want the head cut at 40 runes", response)
	}
}

func TestVideoDiagFieldsKeepTheReplyOnlyForARefusal(t *testing.T) {
	for _, outcome := range []string{"video", "invalid_video_download", "video_not_ready"} {
		response, _ := videoDiagFields(diagFixture(), outcome)["remote_transport"].(string)
		if strings.Contains(response, "reply_head") || strings.Contains(response, "언어 모델") {
			t.Fatalf("outcome %s: response = %q, want no reply text", outcome, response)
		}
	}
}

func TestVideoDiagNeverCarriesWhatTheTurnWasAskedOrCalled(t *testing.T) {
	// The prompt, ids and addresses reach neither the builder nor the observed
	// candidate's recorded form; only the candidate text is kept, and only for a
	// refusal.
	prompt := "SECRET-PROMPT-TEXT"
	diag := newVideoTurnDiag("someone@example.com", "extension", webAccount{}, capability{}, webFramingLandscape, 2)
	diag.observe([]any{[]any{"rc_conversationid"}, nil, "https://example.com/video"})
	fields := videoDiagFields(diag, "video")
	var all strings.Builder
	for _, value := range fields {
		all.WriteString(value.(string))
		all.WriteByte('\n')
	}
	for _, forbidden := range []string{prompt, "someone", "example.com", "conversationid", "rc_", "https://"} {
		if strings.Contains(all.String(), forbidden) {
			t.Fatalf("diagnostic carries %q: %s", forbidden, all.String())
		}
	}
	if !strings.Contains(fields["reason"].(string), "kind=extension") {
		t.Fatalf("reason = %v", fields["reason"])
	}
}

func TestVideoDiagIsNilSafe(t *testing.T) {
	var diag *videoTurnDiag
	diag.observe([]any{nil})
	diag.observeFrame([]byte("[]"))
	service := newService(nil)
	service.reportVideoTurn(diag, "video")
	service.reportVideoTurn(diagFixture(), "video")
	service.stashVideoDiag("k", nil)
	service.dropVideoDiag("k")
	if fields := videoDiagFields(nil, "error"); fields["state"] != "video_turn_diag" {
		t.Fatalf("fields = %v", fields)
	}
}

func TestVideoOutcomeNamesTheCode(t *testing.T) {
	if videoOutcome(nil) != "video" || videoOutcome(webNoVideo("anything")) != "no_video" || videoOutcome(failure(504, "video_not_ready")) != "video_not_ready" {
		t.Fatal("outcomes do not follow the public codes")
	}
}

func TestVideoSlotSkeletonsKeepFormAndNeverStrings(t *testing.T) {
	secret := "SECRET-슬롯-TEXT"
	candidate := make([]any, 38)
	candidate[9] = []any{[]any{secret, float64(3), true, nil}, map[string]any{"topsecret": []any{secret}}, float64(1.5)}
	candidate[28] = []any{[]any{[]any{[]any{[]any{[]any{[]any{secret}}}}}}}
	candidate[37] = []any{secret}

	diag := &videoTurnDiag{account: "none", kind: "first"}
	diag.observe([]any{nil, []any{secret}, candidate[2], candidate[3], candidate[4], candidate[5], candidate[6], candidate[7], candidate[8], candidate[9], nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, candidate[28], nil, nil, nil, nil, nil, nil, nil, nil, candidate[37]})
	response, _ := videoDiagFields(diag, "video")["remote_transport"].(string)

	for _, want := range []string{"s9=[[s14,3,true,null],{s9:[s14]},1.5]", "s37=[s14]", "s28=[[[[[[..]]]]]]"} {
		if !strings.Contains(response, want) {
			t.Fatalf("response = %q, missing %q", response, want)
		}
	}
	for _, forbidden := range []string{"SECRET", "슬롯", "topsecret"} {
		if strings.Contains(response, forbidden) {
			t.Fatalf("response carries %q: %s", forbidden, response)
		}
	}
	if long := videoSkeleton(make([]any, 500)); len([]rune(long)) != skeletonLimit {
		t.Fatalf("skeleton length = %d, want %d", len([]rune(long)), skeletonLimit)
	}
	if got := videoSkeleton(nil); got != "null" {
		t.Fatalf("absent slot = %q", got)
	}
}
