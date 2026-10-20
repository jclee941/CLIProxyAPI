package main

import (
	"slices"
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
		return time.NewTimer(time.Hour)
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
