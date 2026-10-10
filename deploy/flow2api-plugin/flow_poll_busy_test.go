package main

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestFlowVideoWaitsForBrokerLeaseWithoutRepeatingGeneration(t *testing.T) {
	// Given: an accepted generation, with six locally refused status reads.
	fixture := newFlowFixture(t)
	media := []any{flowTestOp, flowTestProject, flowTestMedia}
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("YhhmEf", rpcEnvelope(t, "YhhmEf", []any{[]any{media}}))
	fixture.reply("jwpduf", rpcEnvelope(t, "jwpduf", []any{[]any{append(slices.Clone(media), fixture.link("video"))}}))
	fixture.mu.Lock()
	fixture.busyRPC, fixture.busyRemaining = "jwpduf", 6
	fixture.mu.Unlock()
	service, record := flowService(t, fixture)

	// When: status reading resumes after the shared credential lease is free.
	result := flowExecute(t, service, record, "flow-veo-3.1-fast",
		`{"contents":[{"parts":[{"text":"fixture"}]}],"generationConfig":{"durationSeconds":4}}`)

	// Then: the original media is delivered after one generation submission.
	mime, data := flowInlineMedia(t, result)
	if mime != "video/mp4" || !slices.Equal(data, flowTestMP4) || fixture.count("YhhmEf") != 1 || fixture.count("jwpduf") != 1 {
		t.Fatal("broker contention lost or repeated the accepted generation")
	}
}

func TestFlowImageWaitsForBrokerLeaseBeforeSubmitting(t *testing.T) {
	// Given: a Gemini generation holding the source account's lease for three exchanges.
	fixture := newFlowFixture(t)
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{[]any{flowTestProject}}))
	fixture.reply("ogiZ0b", rpcEnvelope(t, "ogiZ0b", []any{[]any{[]any{flowTestMedia, nil, fixture.link("image")}}}))
	fixture.mu.Lock()
	fixture.busyRPC, fixture.busyRemaining = "ogiZ0b", 3
	fixture.mu.Unlock()
	service, record := flowService(t, fixture)

	// When: the image is requested while the lease is held.
	result := flowExecute(t, service, record, "flow-nano-banana-2", `{"contents":[{"role":"user","parts":[{"text":"a red apple"}]}]}`)

	// Then: it is delivered once the lease is free, from one submission.
	mime, data := flowInlineMedia(t, result)
	if mime != "image/png" || string(data) != string(flowTestPNG) || fixture.count("ogiZ0b") != 1 {
		t.Fatalf("image %s %d bytes after %d submissions", mime, len(data), fixture.count("ogiZ0b"))
	}
}

func TestFlowGenerationReportsBusyLeaseAfterTheWaitBound(t *testing.T) {
	// Given: a lease that stays held past the wait bound.
	fixture := newFlowFixture(t)
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{[]any{flowTestProject}}))
	fixture.reply("ogiZ0b", rpcEnvelope(t, "ogiZ0b", []any{[]any{[]any{flowTestMedia, nil, fixture.link("image")}}}))
	fixture.mu.Lock()
	fixture.busyRPC, fixture.busyRemaining = "ogiZ0b", 1<<30
	fixture.mu.Unlock()
	service, record := flowService(t, fixture)
	clock, waits := time.Unix(1700000000, 0), 0
	service.now = func() time.Time { return clock }
	service.flowWait = func(ctx context.Context, interval time.Duration) error {
		if interval == flowLeasePoll {
			waits++
		}
		clock = clock.Add(interval)
		return ctx.Err()
	}

	// When: the image is requested.
	result := flowExecute(t, service, record, "flow-nano-banana-2", `{"contents":[{"role":"user","parts":[{"text":"a red apple"}]}]}`)

	// Then: the caller hears busy once the bound has passed, and nothing was submitted.
	if result.OK || result.Error.Code != "flow_session_exchange_busy" || result.Error.HTTPStatus != 409 ||
		fixture.count("ogiZ0b") != 0 || waits != int(flowLeaseWait/flowLeasePoll) {
		t.Fatalf("result %+v after %d lease waits and %d submissions", result.Error, waits, fixture.count("ogiZ0b"))
	}
}
