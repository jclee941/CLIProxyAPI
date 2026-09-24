package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestContinuationNeverUploadsOrGeneratesOnAnotherAccount(t *testing.T) {
	service, source := continuationFixture(t)
	target := localRecordFixture(t)
	target.Target = recordFixture(t, "b")
	target.Token, target.State = encodedToken("target-cookie"), localReady
	auth, err := authFromRecord(target.Target)
	if err != nil {
		t.Fatal(err)
	}
	target.Projection = string(auth.StorageJSON)
	if err := service.sessions.write(target); err != nil {
		t.Fatal(err)
	}
	fixture := &continuationWebFixture{video: true}
	continuationWeb(t, service, fixture)
	service.host = (&loginHostFixture{
		records: map[string]json.RawMessage{
			source.Target.ID: jsonFixture(t, source.Target),
			target.Target.ID: jsonFixture(t, target.Target),
		},
		service: service,
	}).call
	first := interactionID(t, interactionCall(t, service, source,
		`{"model":"gemini-omni-1.1-flash","input":"first"}`))
	headers := http.Header{continuationHeader: {first}}
	pick, err := service.pickContinuation(jsonFixture(t, map[string]any{
		"Providers": []string{provider}, "Model": interactionOmniModel,
		"Options":    map[string]any{"Headers": headers, "Metadata": map[string]string{"caller_scope": testCallerScope}},
		"Candidates": []any{map[string]string{"ID": target.Target.ID, "Provider": provider}},
	}))
	if safeCredentialCode(err) != "continuation_account_unavailable" || pick.Handled {
		t.Errorf("unavailable owner did not fail closed: pick=%+v err=%v", pick, err)
	}
	full := 1.0
	reset := float64(service.now().Add(time.Hour).Unix())
	service.observeQuota(source.Target.ID, &usageView{Metrics: []usageMetric{{WindowKind: "5h", UsageFraction: &full, ResetUnixSeconds: &reset}}})
	candidates := slotCandidates(source.Target.ID, target.Target.ID)
	pick, err = service.pickContinuation(jsonFixture(t, map[string]any{
		"Provider": provider, "Model": interactionOmniModel,
		"Options":    map[string]any{"Headers": headers, "Metadata": map[string]string{"caller_scope": testCallerScope}},
		"Candidates": candidates,
	}))
	if safeCredentialCode(err) != "continuation_account_unavailable" || pick.Handled {
		t.Errorf("exhausted owner was selected or replaced: pick=%+v err=%v", pick, err)
	}
	headers.Set(interactionRetrieveHeader, "true")
	pick, err = service.pickContinuation(jsonFixture(t, map[string]any{
		"Provider": provider, "Model": interactionOmniModel,
		"Options":    map[string]any{"Headers": headers, "Metadata": map[string]string{"caller_scope": testCallerScope}},
		"Candidates": candidates,
	}))
	if err != nil || !pick.Handled || pick.AuthID != source.Target.ID {
		t.Errorf("quota exhaustion blocked owner-bound retrieval: pick=%+v err=%v", pick, err)
	}
	for _, extra := range []string{"", `,"stream":true`, `,"generation_config":{"video_config":{"task":"extend"}}`} {
		body := `{"model":"gemini-omni-1.1-flash","input":"continue","previous_interaction_id":"` + first + `"` + extra + `}`
		result := interactionCall(t, service, target, body)
		if result.OK {
			t.Errorf("wrong-account continuation was accepted: options=%s", extra)
		}
	}
	stored, err := service.sessions.read(target.Target.TokenRef)
	if err != nil {
		t.Fatal(err)
	}
	turns, err := continuationTurns(stored)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 0 {
		t.Errorf("wrong-account continuation created %d turns", len(turns))
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.fields) != 1 || len(fixture.uploads) != 0 {
		t.Fatalf("cross-account upstream activity: submissions=%d uploads=%d", len(fixture.fields), len(fixture.uploads))
	}
}

func TestRemovedInternalChainReferenceIsRejected(t *testing.T) {
	payload := []byte(`{"contents":[{"role":"user","parts":[{"text":"continue"},{"fileData":{"fileUri":"interaction:account|key|caller"}}]}]}`)
	if err := validateOmni(payload); err == nil {
		t.Fatal("removed chain reference accepted by Omni validator")
	}
	if _, _, err := webContentsToPrompt(payload); err == nil {
		t.Fatal("removed chain reference accepted by prompt builder")
	}
}
