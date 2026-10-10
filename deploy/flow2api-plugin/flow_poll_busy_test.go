package main

import (
	"slices"
	"testing"
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
