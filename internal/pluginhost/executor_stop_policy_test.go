package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func TestPluginStopPolicyPreservesUnmatchedErrorsAndWireRegex(t *testing.T) {
	original := rpcError{message: "gemini_web_omni:submission_unknown", statusCode: 502}
	for _, test := range []struct {
		name string
		rule map[string]any
		stop bool
	}{
		{"match", map[string]any{"status": 502, "action": "stop", "match": []string{"gemini_web_omni:"}}, true},
		{"wire regex", map[string]any{"status": 502, "action": "stop", "match-regexr": []string{"^gemini_web_omni:"}}, true},
		{"other status", map[string]any{"status": 401, "action": "stop", "match": []string{"gemini_web_omni:"}}, false},
		{"other prefix", map[string]any{"status": 502, "action": "stop", "match": []string{"different:"}}, false},
		{"other action", map[string]any{"status": 502, "action": "continue", "match": []string{"gemini_web_omni:"}}, false},
		{"legacy", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Given an error and credential policy in the upstream wire shape.
			auth := &coreauth.Auth{Metadata: map[string]any{}}
			if test.rule != nil {
				auth.Metadata["request_scoped_errors"] = []any{test.rule}
			}
			// When the adapter classifies it for the existing conductor lifecycle.
			got := pluginRequestStopPolicy(auth, original)
			// Then only matching stop rules change scope; status/cause stay intact.
			var scoped interface{ IsRequestScoped() bool }
			if stop := errors.As(got, &scoped) && scoped.IsRequestScoped(); stop != test.stop {
				t.Fatalf("stop=%v, want %v", stop, test.stop)
			}
			if !errors.Is(got, original) {
				t.Fatal("original error lost")
			}
		})
	}
}

func TestPluginCredentialStopPolicyPreventsGenerationFailover(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			// Given two eligible accounts and a plugin-owned stop rule for paid generation.
			var calls atomic.Int32
			upstreamErr := rpcError{message: "gemini_web_omni:submission_unknown", statusCode: 502}
			executor := &fakeExecutor{execute: func(context.Context, pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
				calls.Add(1)
				return pluginapi.ExecutorResponse{}, upstreamErr
			}, executeStream: func(context.Context, pluginapi.ExecutorRequest) (pluginapi.ExecutorStreamResponse, error) {
				calls.Add(1)
				return pluginapi.ExecutorStreamResponse{}, upstreamErr
			}}
			host := New()
			adapter := newCurrentExecutorAdapterForTest(host, "stop-policy", executor, []sdktranslator.Format{sdktranslator.FormatGemini}, []sdktranslator.Format{sdktranslator.FormatGemini})
			manager := coreauth.NewManager(nil, nil, nil)
			manager.RegisterExecutor(adapter)
			model := fmt.Sprintf("plugin-stop-policy-%v", stream)
			for _, id := range []string{"stop-account-a", "stop-account-b"} {
				auth := &coreauth.Auth{ID: id, Provider: adapter.Identifier(), Status: coreauth.StatusActive, Metadata: map[string]any{"request_scoped_errors": []any{map[string]any{"status": 502, "match": []string{"gemini_web_omni:"}, "action": "stop"}}}}
				if _, err := manager.Register(t.Context(), auth); err != nil {
					t.Fatal(err)
				}
				registry.GetGlobalRegistry().RegisterClient(id, auth.Provider, []*registry.ModelInfo{{ID: model}})
				t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(id) })
			}
			// When the first account reports an ambiguous generation failure.
			req := coreexecutor.Request{Model: model, Payload: []byte(`{"contents":[{"parts":[{"text":"video"}]}]}`)}
			opts := coreexecutor.Options{SourceFormat: sdktranslator.FormatGemini, Stream: stream}
			var err error
			if stream {
				_, err = manager.ExecuteStream(t.Context(), []string{adapter.Identifier()}, req, opts)
			} else {
				_, err = manager.Execute(t.Context(), []string{adapter.Identifier()}, req, opts)
			}
			// Then the same request never reaches a second credential or cools either one.
			if err == nil {
				t.Fatal("lost generation error")
			}
			if calls.Load() != 1 {
				t.Fatalf("submissions=%d, want exactly one", calls.Load())
			}
			for _, auth := range manager.List() {
				if auth.Unavailable {
					t.Fatalf("request-local error cooled %s", auth.ID)
				}
				for _, state := range auth.ModelStates {
					if state.Unavailable {
						t.Fatalf("request-local error cooled model on %s", auth.ID)
					}
				}
			}
		})
	}
}
