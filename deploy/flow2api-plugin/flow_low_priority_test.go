package main

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestFlowLowPriorityNeverSpendsCreditsWithoutOptIn(t *testing.T) {
	fixture := newFlowFixture(t)
	fixture.replies["nzlxg"] = []string{rpcEnvelope(t, "nzlxg", []any{flowFallbackCreditFloor + 10000, 2, 3, 3})}
	media := []any{flowTestOp, flowTestProject, flowTestMedia}
	refused := flowErrorEnvelope(t, []any{"wrb.fr", "YhhmEf", nil, nil, nil, []any{3, nil, []any{[]any{"type.googleapis.com/google.rpc.ErrorInfo", []any{"PUBLIC_ERROR_UNUSUAL_ACTIVITY"}}}}, "generic"})
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("YhhmEf", refused, refused, refused, rpcEnvelope(t, "YhhmEf", []any{[]any{media}}))
	fixture.reply("jwpduf", rpcEnvelope(t, "jwpduf", []any{[]any{append(slices.Clone(media), fixture.link("video"))}}))
	service, record := flowService(t, fixture)
	clock := time.Unix(1700000000, 0)
	service.now = func() time.Time { return clock }
	payload := `{"contents":[{"parts":[{"text":"a fox"}]}],"generationConfig":{"durationSeconds":8,"flow":{"priority":"low"}}}`

	first := flowExecute(t, service, record, "flow-veo-3.1-lite", payload)
	second := flowExecute(t, service, record, "flow-veo-3.1-lite", payload)

	for _, result := range []envelope{first, second} {
		if result.OK || result.Error == nil || result.Error.Code != "flow_paid_fallback_disabled" || result.Error.HTTPStatus != 429 {
			t.Fatalf("free request was not kept free: %+v", result.Error)
		}
	}
	if fixture.count("YhhmEf") != 3 {
		t.Fatalf("refusal or paused request spent another submission: %d", fixture.count("YhhmEf"))
	}
	if _, held := service.unusualActivityHeld(record.SourceAuthID); held {
		t.Fatal("a free-queue refusal held unrelated account operations")
	}

	clock = clock.Add(flowLowPriorityPause + time.Second)
	resumed := flowExecute(t, service, record, "flow-veo-3.1-lite", payload)
	if mime, data := flowInlineMedia(t, resumed); mime != "video/mp4" || !slices.Equal(data, flowTestMP4) {
		t.Fatalf("free queue did not resume: %s %d bytes", mime, len(data))
	}
	if fixture.count("YhhmEf") != 4 {
		t.Fatalf("resumed submissions = %d", fixture.count("YhhmEf"))
	}
	for attempt := 0; attempt < 4; attempt++ {
		key, _ := jsonField(fixture.args("YhhmEf", attempt), 0, 0, 1).(string)
		if !strings.HasSuffix(key, "_low_priority") {
			t.Fatalf("submission %d spent paid priority: %q", attempt, key)
		}
	}
}
