package pluginhost

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers/gemini"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type omniBootstrapRPCClient struct {
	*omniHTTPRPCClient
	picks   atomic.Int32
	streams atomic.Int32
}

func (client *omniBootstrapRPCClient) Call(ctx context.Context, method string, raw []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodSchedulerPick:
		client.picks.Add(1)
	case pluginabi.MethodExecutorExecuteStream:
		client.streams.Add(1)
	}
	return client.omniHTTPRPCClient.Call(ctx, method, raw)
}

type omniResultHook struct {
	coreauth.NoopHook
	results chan coreauth.Result
}

func (hook *omniResultHook) OnResult(_ context.Context, result coreauth.Result) {
	hook.results <- result
}

func TestOmniInteractionResumeBootstrapKeepsRetrievalErrorWithoutCredentialRetry(t *testing.T) {
	for _, scenario := range []struct{ mode, code, status string }{
		{"pending", "interaction_pending_retrieve_receipt", "in_progress"},
		{"ended", "missing_upstream_operation", "failed"},
		{"mismatch", "continuation_operation_mismatch", ""},
		{"unauthorized", "auth_error", ""},
	} {
		t.Run(scenario.mode, func(t *testing.T) {
			// Given the actual plugin, stream bridge, two eligible credentials and
			// host bootstrap retries enabled, with an interrupted receipt to recover.
			t.Setenv("CPA_OMNI_FIXTURE_RETRIEVAL", scenario.mode)
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			t.Cleanup(cancel)
			host := New()
			client := &omniBootstrapRPCClient{omniHTTPRPCClient: startOmniRPCFixture(t, ctx, host)}
			plugin, err := registerRPCPlugin(ctx, host, "gemini-web", client, pluginabi.MethodPluginRegister, []byte("native_generation: true\nnative_continuation: true\n"))
			if err != nil {
				t.Fatal(err)
			}
			record := normalizeTestCapabilityRecord(capabilityRecord{id: "gemini-web", plugin: plugin})
			setHostSnapshotForTest(host, true, record)
			results := make(chan coreauth.Result, 8)
			manager := coreauth.NewManager(nil, nil, &omniResultHook{results: results})
			manager.SetPluginScheduler(host)
			host.SetAuthManager(manager)
			manager.RegisterExecutor(newExecutorAdapterRegistration(host, record, "gemini-web", plugin.Capabilities.Executor).adapter)
			raw, err := client.Call(ctx, "auth", nil)
			if err != nil {
				t.Fatal(err)
			}
			var data pluginapi.AuthData
			if err := json.Unmarshal(raw, &data); err != nil {
				t.Fatal(err)
			}
			auth := host.AuthDataToCoreAuth(data, "", data.ID)
			if _, err := manager.Register(ctx, auth); err != nil {
				t.Fatal(err)
			}
			const model = "gemini-omni-1.1-flash"
			registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: model}})
			t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
			base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{Streaming: sdkconfig.StreamingConfig{BootstrapRetries: 1}}, manager)
			base.SetPluginHost(host)
			handler := gemini.NewGeminiAPIHandler(base)
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.Use(func(c *gin.Context) { c.Set("userApiKey", "caller-a") })
			router.POST("/v1beta/interactions", handler.Interactions)
			router.GET("/v1beta/interactions/:id", handler.RetrieveInteraction)
			call := func(method, path, body string) *httptest.ResponseRecorder {
				t.Helper()
				request := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				return response
			}
			if _, err := client.Call(ctx, "release", nil); err != nil {
				t.Fatal(err)
			}
			post := call(http.MethodPost, "/v1beta/interactions", `{"model":"`+model+`","input":"first","store":true}`)
			var receipt struct{ ID, Status string }
			if err := json.Unmarshal(post.Body.Bytes(), &receipt); err != nil {
				t.Fatal(err)
			}
			if post.Code != http.StatusOK || receipt.ID == "" || receipt.Status != "in_progress" {
				t.Fatalf("POST %d: %s", post.Code, post.Body.String())
			}
			if result := omniSignal(t, ctx, results); !result.Success || result.AuthID != auth.ID {
				t.Fatalf("POST execution result: %+v", result)
			}
			if scenario.mode == "ended" {
				// End the fixture's named turn in its authenticated store, retaining
				// its old handles but no active key or stored video, as after release.
				if _, err := client.Call(ctx, "end", nil); err != nil {
					t.Fatal(err)
				}
			}
			// A second candidate keeps the scheduler reachable after the host
			// excludes the owner. It must never execute this account's receipt.
			other := auth.Clone()
			other.ID = "gemini-web-other.json"
			if _, err := manager.Register(ctx, other); err != nil {
				t.Fatal(err)
			}
			registry.GetGlobalRegistry().RegisterClient(other.ID, other.Provider, []*registry.ModelInfo{{ID: model}})
			t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(other.ID) })
			picks := client.picks.Load()
			// When GET resumes after the receipt event and recovery ends pending/failed.
			response := call(http.MethodGet, "/v1beta/interactions/"+receipt.ID+"?stream=true&last_event_id="+receipt.ID+":1", "")
			// Then native bytes establish the stream, retaining the original error
			// with no re-selection, no duplicate cursor, and no new generation.
			attempts := client.picks.Load() - picks
			if attempts != 1 || client.streams.Load() != 1 {
				t.Errorf("GET retried credentials: scheduler picks=%d stream executions=%d; HTTP %d: %s", attempts, client.streams.Load(), response.Code, response.Body.String())
			}
			if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream" || !strings.HasPrefix(response.Body.String(), ":") {
				t.Errorf("GET bootstrap: HTTP %d: %s", response.Code, response.Body.String())
			} else {
				scanner := bufio.NewScanner(strings.NewReader(response.Body.String()))
				event := readOmniSSE(t, scanner)
				if scenario.status == "failed" {
					if event.Name != "interaction.failed" {
						t.Errorf("ended receipt lacks terminal event: %+v", event)
					} else {
						var failed struct {
							Interaction struct {
								ID, Status string
								Error      struct{ Code, Message string }
							}
						}
						if err := json.Unmarshal(event.Data, &failed); err != nil {
							t.Fatal(err)
						}
						if event.ID != receipt.ID+":2" || failed.Interaction.ID != receipt.ID || failed.Interaction.Status != "failed" || failed.Interaction.Error.Code != scenario.code || failed.Interaction.Error.Message != scenario.code {
							t.Errorf("terminal receipt contract: %+v", failed)
						}
						event = readOmniSSE(t, scanner)
					}
				}
				var failure struct {
					Error       struct{ Message, Type, Code string } `json:"error"`
					Interaction struct{ ID, Status string }          `json:"interaction"`
				}
				if err := json.Unmarshal(event.Data, &failure); err != nil {
					t.Fatal(err)
				}
				if event.Name != "error" || event.ID != "" || failure.Error.Message != scenario.code || failure.Error.Type != "server_error" || failure.Error.Code != "internal_server_error" {
					t.Errorf("retrieval error: %+v, error=%+v", event, failure.Error)
				}
				if scenario.status != "" && (failure.Interaction.ID != receipt.ID || failure.Interaction.Status != scenario.status) {
					t.Errorf("interaction outcome lost: %+v", failure.Interaction)
				}
				for scanner.Scan() {
					if scanner.Text() != "" {
						t.Errorf("unexpected replay output: %q", scanner.Text())
					}
				}
				if err := scanner.Err(); err != nil {
					t.Fatal(err)
				}
			}
			result := omniSignal(t, ctx, results)
			updated, found := manager.GetByID(auth.ID)
			if !found || updated.ModelStates[model] == nil {
				t.Fatal("missing owner/model execution state")
			}
			state := updated.ModelStates[model]
			if unused, found := manager.GetByID(other.ID); !found || unused.Unavailable || unused.Success != 0 || unused.Failed != 0 {
				t.Error("retrieval affected a different account")
			}
			if scenario.status != "" {
				if !result.Success || result.Error != nil || result.AuthID != auth.ID {
					t.Errorf("outcome recorded as credential failure: %+v", result)
				}
				if updated.Unavailable || !updated.NextRetryAfter.IsZero() || updated.LastError != nil || updated.StatusMessage != "" || updated.Failed != 0 || state.Unavailable || !state.NextRetryAfter.IsZero() || state.LastError != nil || state.StatusMessage != "" {
					t.Errorf("outcome penalized owner: unavailable=%t status_message=%q failed=%d retry_after=%v model_unavailable=%t model_retry_after=%v cooldown=%v", updated.Unavailable, updated.StatusMessage, updated.Failed, updated.NextRetryAfter, state.Unavailable, state.NextRetryAfter, state.NextRetryAfter.Sub(state.UpdatedAt))
				}
				// The same receipt must remain retrievable immediately, without a
				// cooldown expiry, account switch, or another generation submission.
				retrieved := call(http.MethodGet, "/v1beta/interactions/"+receipt.ID, "")
				var again struct {
					ID, Status string
					Error      struct{ Code, Message string }
				}
				if err := json.Unmarshal(retrieved.Body.Bytes(), &again); err != nil {
					t.Fatal(err)
				}
				if retrieved.Code != http.StatusOK || again.ID != receipt.ID || again.Status != scenario.status || (scenario.status == "failed" && (again.Error.Code != scenario.code || again.Error.Message != scenario.code)) {
					t.Errorf("receipt outcome blocked: HTTP %d: %s", retrieved.Code, retrieved.Body.String())
				} else if result := omniSignal(t, ctx, results); !result.Success || result.AuthID != auth.ID {
					t.Errorf("receipt switched accounts: %+v", result)
				}
			} else if result.Success || result.Error == nil || result.Error.Message != scenario.code || !updated.Unavailable || state.NextRetryAfter.Sub(state.UpdatedAt) != time.Minute {
				t.Errorf("real failure lost: result=%+v unavailable=%t model_state=%+v", result, updated.Unavailable, state)
			}
			stats, err := client.Call(ctx, "stats", nil)
			if err != nil {
				t.Fatal(err)
			}
			var submissions []json.RawMessage
			if err := json.Unmarshal(stats, &submissions); err != nil {
				t.Fatal(err)
			}
			if len(submissions) != 1 {
				t.Fatalf("GET resubmitted generation: submissions=%d", len(submissions))
			}
		})
	}
}
