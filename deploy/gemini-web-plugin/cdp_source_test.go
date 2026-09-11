package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func assertCDPError(t *testing.T, err error, code string) {
	t.Helper()
	var public *publicError
	statuses := map[string]int{"source_unavailable": 503, "source_ambiguous": 409, "source_identity_unavailable": 412, "source_identity_changed": 409, "source_protocol_failed": 502, "source_cancelled": 499}
	if !errors.As(err, &public) || public.Code != code || public.Message != code || public.HTTPStatus != statuses[code] {
		t.Fatalf("expected safe %s, received %v", code, err)
	}
}

func TestCDPSourceCaptures_whenTargetsAreFreshOrDuplicated(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		targets  []fakeCDPTarget
		authUser uint64
	}{
		{"fresh-target-id", []fakeCDPTarget{fakeTarget("fresh-tab", "fresh-context", fakeGaia)}, 0},
		{"duplicate-tabs", []fakeCDPTarget{fakeTarget("z-tab", "context", fakeGaia), fakeTarget("a-tab", "context", fakeGaia)}, 0},
		{"account-one", []fakeCDPTarget{func() fakeCDPTarget {
			target := fakeTarget("tab-1", "context", fakeGaia)
			target.URL = "https://gemini.google.com/u/1/app"
			return target
		}()}, 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fake := &fakeCDP{targets: scenario.targets}
			source := fake.source(t)
			request := fakeCaptureRequest(scenario.authUser)

			captured, err := source.Capture(t.Context(), request)

			if err != nil {
				t.Fatal(err)
			}
			if captured.AccountSHA256 != request.Binding.ExpectedGaiaSHA256 {
				t.Fatal("wrong bound account")
			}
			payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(captured.Token.value, "gemini-web:v1:"))
			if err != nil {
				t.Fatal("invalid token encoding")
			}
			var decoded struct {
				Cookie   string `json:"cookie"`
				AuthUser uint64 `json:"auth_user"`
			}
			if json.Unmarshal(payload, &decoded) != nil || decoded.Cookie != "SID=synthetic-session" || decoded.AuthUser != scenario.authUser {
				t.Fatal("token did not preserve cookie and auth user")
			}
			awaitFakeClose(t, fake)
			if fake.cookies.Load() != 1 || fake.attached.Load() != fake.detached.Load() {
				t.Fatal("capture/cleanup count mismatch")
			}
		})
	}
}

func TestCDPSourceFailsClosed_whenIdentitySelectionIsUnsafe(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		targets []fakeCDPTarget
		code    string
	}{
		{"same-account-two-contexts", []fakeCDPTarget{fakeTarget("a", "one", fakeGaia), fakeTarget("b", "two", fakeGaia)}, "source_ambiguous"},
		{"shared-context-configured-accounts", []fakeCDPTarget{fakeTarget("a", "one", fakeGaia), fakeTarget("b", "one", otherFakeGaia)}, "source_ambiguous"},
		{"missing-identity", []fakeCDPTarget{fakeTarget("a", "one", "")}, "source_identity_unavailable"},
		{"invalid-identity", []fakeCDPTarget{fakeTarget("a", "one", "not-an-account")}, "source_identity_unavailable"},
		{"conflicting-identity", []fakeCDPTarget{func() fakeCDPTarget {
			target := fakeTarget("a", "one", fakeGaia)
			target.Identity[1] = otherFakeGaia
			return target
		}()}, "source_identity_unavailable"},
		{"other-account-only", []fakeCDPTarget{fakeTarget("a", "one", otherFakeGaia)}, "source_identity_unavailable"},
		{"missing-context", []fakeCDPTarget{fakeTarget("a", "", fakeGaia)}, "source_unavailable"},
		{"missing-target", nil, "source_unavailable"},
		{"foreign-origin", []fakeCDPTarget{func() fakeCDPTarget {
			target := fakeTarget("a", "one", fakeGaia)
			target.URL = "https://accounts.google.com/app"
			return target
		}()}, "source_unavailable"},
		{"wrong-auth-user", []fakeCDPTarget{func() fakeCDPTarget {
			target := fakeTarget("a", "one", fakeGaia)
			target.URL = "https://gemini.google.com/u/1/app"
			return target
		}()}, "source_unavailable"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fake := &fakeCDP{targets: scenario.targets}
			source := fake.source(t)

			_, err := source.Capture(t.Context(), fakeCaptureRequest(0))

			assertCDPError(t, err, scenario.code)
			awaitFakeClose(t, fake)
			if fake.cookies.Load() != 0 || fake.attached.Load() != fake.detached.Load() {
				t.Fatal("unsafe cookie read or session leak")
			}
		})
	}
}

func TestCDPSourceRejects_whenIdentityChangesAfterCookies(t *testing.T) {
	for _, mutate := range []struct {
		name   string
		change func(*fakeCDPTarget)
	}{
		{"account", func(target *fakeCDPTarget) { target.Identity = [3]string{otherFakeGaia, otherFakeGaia, otherFakeGaia} }},
		{"context", func(target *fakeCDPTarget) { target.Context = "swapped-context" }},
		{"origin", func(target *fakeCDPTarget) { target.URL = "https://accounts.google.com/app" }},
		{"auth-user", func(target *fakeCDPTarget) { target.URL = "https://gemini.google.com/u/1/app" }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			fake := &fakeCDP{targets: []fakeCDPTarget{fakeTarget("a", "one", fakeGaia)}, afterCookies: mutate.change}
			source := fake.source(t)

			captured, err := source.Capture(t.Context(), fakeCaptureRequest(0))

			assertCDPError(t, err, "source_identity_changed")
			awaitFakeClose(t, fake)
			if captured.Token.value != "" || fake.cookies.Load() != 1 || fake.attached.Load() != fake.detached.Load() {
				t.Fatal("changed capture escaped or session leaked")
			}
		})
	}
}

func TestCDPSourceRediscovers_whenRuntimeIDsChange(t *testing.T) {
	fake := &fakeCDP{targets: []fakeCDPTarget{fakeTarget("old", "old-context", fakeGaia)}}
	source := fake.source(t)
	first, err := source.Capture(t.Context(), fakeCaptureRequest(0))
	if err != nil {
		t.Fatal(err)
	}
	awaitFakeClose(t, fake)
	fake.targets = []fakeCDPTarget{fakeTarget("new", "new-context", fakeGaia)}

	second, err := source.Capture(t.Context(), fakeCaptureRequest(0))

	if err != nil || second.AccountSHA256 != first.AccountSHA256 || second.Token != first.Token {
		t.Fatal("runtime ID change broke stable account binding")
	}
	awaitFakeClose(t, fake)
	if fake.cookies.Load() != 2 || fake.attached.Load() != fake.detached.Load() {
		t.Fatal("fresh capture did not clean up")
	}
}

func TestCDPSourceRejectsCookies_whenASCIIOrEncodedLengthIsInvalid(t *testing.T) {
	for _, value := range []string{"invalid\nvalue", "non-ascii-\u00e9", "bad;cookie=injected", strings.Repeat("x", 32768)} {
		t.Run("invalid-cookie", func(t *testing.T) {
			fake := &fakeCDP{targets: []fakeCDPTarget{fakeTarget("tab", "context", fakeGaia)}, cookieValue: value}
			source := fake.source(t)

			captured, err := source.Capture(t.Context(), fakeCaptureRequest(0))

			assertCDPError(t, err, "source_protocol_failed")
			awaitFakeClose(t, fake)
			if captured.Token.value != "" {
				t.Fatal("invalid cookie token escaped")
			}
		})
	}
}

func TestCDPSourceUsesVerifiedContext_whenDiscoveryOmitsIt(t *testing.T) {
	fake := &fakeCDP{targets: []fakeCDPTarget{fakeTarget("tab", "", fakeGaia)}, infoContext: "verified-context"}
	source := fake.source(t)

	captured, err := source.Capture(t.Context(), fakeCaptureRequest(0))

	if err != nil || captured.AccountSHA256 != fakeCaptureRequest(0).Binding.ExpectedGaiaSHA256 {
		t.Fatal("verified target context was not used")
	}
	awaitFakeClose(t, fake)
}

func TestCDPSourceRejectsSharedJar_whenOtherBindingUsesAnotherAuthUser(t *testing.T) {
	other := fakeTarget("other", "context", otherFakeGaia)
	other.URL = "https://gemini.google.com/u/1/app"
	fake := &fakeCDP{targets: []fakeCDPTarget{fakeTarget("requested", "context", fakeGaia), other}}
	source := fake.source(t)
	request := fakeCaptureRequest(0)
	otherBinding := request.Bindings["two"]
	index := uint64(1)
	otherBinding.AuthUser = &index
	request.Bindings["two"] = otherBinding

	_, err := source.Capture(t.Context(), request)

	assertCDPError(t, err, "source_ambiguous")
	awaitFakeClose(t, fake)
	if fake.cookies.Load() != 0 {
		t.Fatal("shared jar cookies accessed")
	}
}

func TestCDPSourceRechecksEveryTab_whenPeerChangesDuringCapture(t *testing.T) {
	fake := &fakeCDP{targets: []fakeCDPTarget{fakeTarget("z-peer", "context", fakeGaia), fakeTarget("a-selected", "context", fakeGaia)}}
	fake.afterCookies = func(target *fakeCDPTarget) {
		if target.ID != "a-selected" {
			t.Error("selection is not deterministic")
		}
		fake.targets[0].Identity = [3]string{otherFakeGaia, otherFakeGaia, otherFakeGaia}
	}
	source := fake.source(t)

	_, err := source.Capture(t.Context(), fakeCaptureRequest(0))

	assertCDPError(t, err, "source_identity_changed")
	awaitFakeClose(t, fake)
}

func TestCDPSourceRejectsSharedJar_whenBindingIndexIsUnspecified(t *testing.T) {
	other := fakeTarget("other", "context", otherFakeGaia)
	other.URL = "https://gemini.google.com/u/1/app"
	fake := &fakeCDP{targets: []fakeCDPTarget{fakeTarget("requested", "context", fakeGaia), other}}
	source := fake.source(t)

	_, err := source.Capture(t.Context(), fakeCaptureRequest(0))

	assertCDPError(t, err, "source_ambiguous")
	awaitFakeClose(t, fake)
	if fake.cookies.Load() != 0 {
		t.Fatal("unindexed binding bypassed shared jar detection")
	}
}

func TestCDPTargetEligibility_whenOriginOrAccountPathDiffers(t *testing.T) {
	for _, scenario := range []struct {
		url   string
		index uint64
		valid bool
	}{
		{"https://gemini.google.com/app", 0, true},
		{"https://gemini.google.com/u/0/app", 0, true},
		{"https://gemini.google.com/u/1/app", 1, true},
		{"https://gemini.google.com/u/18446744073709551615/app", 18446744073709551615, true},
		{"https://gemini.google.com/u/18446744073709551616/app", 0, false},
		{"https://gemini.google.com/u/01/app", 0, false},
		{"https://gemini.google.com/u/-1/app", 0, false},
		{"https://gemini.google.com/u/+1/app", 0, false},
		{"https://gemini.google.com/u/", 0, false},
		{"https://gemini.google.com/u", 0, false},
		{"https://gemini.google.com/%75/1/app", 0, false},
		{"https://gemini.google.com.evil.invalid/app", 0, false},
		{"http://gemini.google.com/app", 0, false},
		{"https://private@gemini.google.com/app", 0, false},
		{"https://gemini.google.com:443/app", 0, false},
	} {
		t.Run(scenario.url, func(t *testing.T) {
			target := cdpTarget{Type: "page", URL: scenario.url}

			index, valid := geminiTargetAuthUser(target)

			if valid != scenario.valid || (valid && index != scenario.index) {
				t.Fatal("target scope parsing mismatch")
			}
		})
	}
}

func TestCDPSourceRejects_whenRuntimeIdentityIsNotThreeExactStrings(t *testing.T) {
	for _, scenario := range []struct{ name, value, code string }{
		{"missing", `{"origin":"https://gemini.google.com"}`, "source_identity_unavailable"},
		{"non-string", `{"origin":"https://gemini.google.com","S06Grb":111111111111111111111,"W3Yyqf":"111111111111111111111","qDCSke":"111111111111111111111"}`, "source_protocol_failed"},
		{"null", `{"origin":"https://gemini.google.com","S06Grb":null,"W3Yyqf":null,"qDCSke":null}`, "source_identity_unavailable"},
		{"foreign-runtime-origin", `{"origin":"https://accounts.google.com","S06Grb":"111111111111111111111","W3Yyqf":"111111111111111111111","qDCSke":"111111111111111111111"}`, "source_identity_unavailable"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fake := &fakeCDP{targets: []fakeCDPTarget{fakeTarget("tab", "context", fakeGaia)}, rawReply: map[string]string{"Runtime.evaluate": `{"id":4,"sessionId":"session-tab","result":{"result":{"type":"object","value":` + scenario.value + `}}}`}}
			source := fake.source(t)

			_, err := source.Capture(t.Context(), fakeCaptureRequest(0))

			assertCDPError(t, err, scenario.code)
			awaitFakeClose(t, fake)
			if fake.cookies.Load() != 0 {
				t.Fatal("invalid runtime identity permitted cookies")
			}
		})
	}
}

func TestCDPSourceSupportsParallelWorkers_whenCapturesShareSource(t *testing.T) {
	fake := &fakeCDP{targets: []fakeCDPTarget{fakeTarget("tab", "context", fakeGaia)}}
	source := fake.source(t)
	request := fakeCaptureRequest(0)
	results := make(chan error, 8)

	for range 8 {
		go func() {
			captured, err := source.Capture(t.Context(), request)
			if err == nil && captured.AccountSHA256 != request.Binding.ExpectedGaiaSHA256 {
				err = errors.New("wrong capture identity")
			}
			results <- err
		}()
	}

	for range 8 {
		if err := <-results; err != nil {
			t.Error(err)
		}
		awaitFakeClose(t, fake)
	}
	if fake.cookies.Load() != 8 || fake.attached.Load() != fake.detached.Load() {
		t.Fatal("parallel capture leaked or shared sessions")
	}
}
