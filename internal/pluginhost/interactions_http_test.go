package pluginhost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

type omniHTTPRPCClient struct {
	url        string
	subscribed chan struct{}
}

func (client *omniHTTPRPCClient) Call(ctx context.Context, method string, raw []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, client.url+"/"+method, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(resp.Body)
	if err := resp.Body.Close(); err != nil {
		return nil, err
	}
	if method == pluginabi.MethodExecutorExecuteStream && bytes.Contains(raw, []byte(`"Alt":"interaction.get"`)) {
		client.subscribed <- struct{}{}
	}
	return body, readErr
}
func (*omniHTTPRPCClient) Shutdown() {}

func omniSignal[T any](t *testing.T, ctx context.Context, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-ctx.Done():
		t.Fatal("Omni HTTP fixture timed out")
		var zero T
		return zero
	}
}

func startOmniRPCFixture(t *testing.T, ctx context.Context, host *Host) *omniHTTPRPCClient {
	t.Helper()
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		body, err := host.callFromPlugin(r.Context(), strings.TrimPrefix(r.URL.Path, "/"), raw)
		if err != nil {
			body = []byte(`{"ok":false}`)
		}
		if _, err := w.Write(body); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(callback.Close)
	binary := filepath.Join(t.TempDir(), "omni-plugin-fixture.test")
	build := exec.CommandContext(ctx, "go", "test", "-race", "-c", "-o", binary, ".")
	build.Dir = filepath.Join("..", "..", "deploy", "gemini-web-plugin")
	build.Env = append(os.Environ(), "GOMAXPROCS=4")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build plugin fixture: %v\n%s", err, output)
	}
	command := exec.CommandContext(ctx, binary, "-test.v", "-test.run=^TestInteractionRPCFixtureProcess$")
	command.Env = append(os.Environ(), "CPA_OMNI_FIXTURE_CALLBACK="+callback.URL, "GOMAXPROCS=4")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	scanned := make(chan struct{})
	var output strings.Builder
	go func() {
		defer close(scanned)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			output.WriteString(line + "\n")
			if strings.HasPrefix(line, "CPA_OMNI_FIXTURE_READY ") {
				ready <- strings.TrimPrefix(line, "CPA_OMNI_FIXTURE_READY ")
			}
		}
		if err := scanner.Err(); err != nil {
			output.WriteString(err.Error())
		}
	}()
	t.Cleanup(func() {
		if err := stdin.Close(); err != nil {
			t.Error(err)
		}
		omniSignal(t, ctx, scanned)
		if err := command.Wait(); err != nil {
			t.Errorf("plugin fixture: %v\n%s\n%s", err, output.String(), stderr.String())
		}
	})
	return &omniHTTPRPCClient{url: omniSignal(t, ctx, ready), subscribed: make(chan struct{}, 8)}
}

type omniSSE struct {
	Name, ID string
	Data     json.RawMessage
}

func readOmniSSE(t *testing.T, scanner *bufio.Scanner) omniSSE {
	t.Helper()
	var event omniSSE
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" && event.Name != "" {
			return event
		}
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			event.Name = v
		}
		if v, ok := strings.CutPrefix(line, "id: "); ok {
			event.ID = v
		}
		if v, ok := strings.CutPrefix(line, "data: "); ok {
			event.Data = []byte(v)
		}
	}
	t.Fatalf("missing SSE event: %v", scanner.Err())
	return event
}

func TestOmniInteractionsHTTPDisconnectAndDurableReplay(t *testing.T) {
	// Given real host handlers, auth selection, plugin RPC adapter/stream bridge,
	// encrypted plugin receipts and a faithful upstream HTTP fixture.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	host := New()
	client := startOmniRPCFixture(t, ctx, host)
	plugin, err := registerRPCPlugin(ctx, host, "gemini-web", client, pluginabi.MethodPluginRegister, []byte("native_generation: true\nnative_continuation: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	record := normalizeTestCapabilityRecord(capabilityRecord{id: "gemini-web", plugin: plugin})
	setHostSnapshotForTest(host, true, record)
	manager := coreauth.NewManager(nil, nil, nil)
	manager.SetPluginScheduler(host)
	host.SetAuthManager(manager)
	manager.RegisterExecutor(newExecutorAdapterRegistration(host, record, "gemini-web", plugin.Capabilities.Executor).adapter)
	response, err := http.Get(client.url + "/auth")
	if err != nil {
		t.Fatal(err)
	}
	var data pluginapi.AuthData
	if err := json.NewDecoder(response.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	auth := host.AuthDataToCoreAuth(data, "", data.ID)
	if _, err := manager.Register(ctx, auth); err != nil {
		t.Fatal(err)
	}
	const model = "gemini-omni-1.1-flash"
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	base.SetPluginHost(host)
	handler := gemini.NewGeminiAPIHandler(base)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		key := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if key != "caller-a" && key != "caller-b" {
			c.AbortWithStatus(401)
			return
		}
		c.Set("userApiKey", key)
	})
	router.POST("/v1beta/interactions", handler.Interactions)
	router.GET("/v1beta/interactions/:id", handler.RetrieveInteraction)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	call := func(method, path, body, key string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	consume := func(res *http.Response) []byte {
		t.Helper()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := res.Body.Close(); err != nil {
			t.Fatal(err)
		}
		return body
	}
	// When a streaming POST yields a receipt while upstream submission is blocked.
	post := call("POST", "/v1beta/interactions", `{"model":"`+model+`","input":"first","stream":true,"store":true,"response_format":{"type":"video","aspect_ratio":"16:9","delivery":"inline"}}`, "caller-a")
	if post.StatusCode != 200 {
		t.Fatalf("POST %d: %s", post.StatusCode, consume(post))
	}
	created := readOmniSSE(t, bufio.NewScanner(post.Body))
	var first struct {
		Interaction struct {
			ID string `json:"id"`
		} `json:"interaction"`
	}
	if err := json.Unmarshal(created.Data, &first); err != nil {
		t.Fatal(err)
	}
	id := first.Interaction.ID
	if post.Header.Get("Content-Type") != "text/event-stream" || post.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("stream headers: %v", post.Header)
	}
	if created.Name != "interaction.created" || created.ID != id+":1" {
		t.Fatalf("created: %+v", created)
	}
	if err := post.Body.Close(); err != nil {
		t.Fatal(err)
	}
	getPath := "/v1beta/interactions/" + id
	pending := call("GET", getPath, "", "caller-a")
	if body := consume(pending); pending.StatusCode != 200 || !bytes.Contains(body, []byte(`"in_progress"`)) {
		t.Fatalf("pending %d: %s", pending.StatusCode, body)
	}
	forbidden := call("GET", getPath, "", "caller-b")
	if body := consume(forbidden); forbidden.StatusCode != 404 {
		t.Fatalf("caller binding %d: %s", forbidden.StatusCode, body)
	}
	missing := call("GET", "/v1beta/interactions/"+strings.Repeat("0", 64), "", "caller-a")
	if body := consume(missing); missing.StatusCode != 404 {
		t.Fatalf("missing %d: %s", missing.StatusCode, body)
	}
	reconnected := make(chan *http.Response, 1)
	go func() { reconnected <- call("GET", getPath+"?stream=true&last_event_id="+id+":1", "", "caller-a") }()
	omniSignal(t, ctx, client.subscribed)
	released, err := http.Get(client.url + "/release")
	if err != nil {
		t.Fatal(err)
	}
	consume(released)
	resumed := omniSignal(t, ctx, reconnected)
	if resumed.StatusCode != 200 {
		t.Fatalf("reconnect %d: %s", resumed.StatusCode, consume(resumed))
	}
	scanner := bufio.NewScanner(resumed.Body)
	for n, name := range []string{"step.start", "step.delta", "step.stop", "interaction.completed", "done"} {
		event := readOmniSSE(t, scanner)
		if event.Name != name || event.ID != fmt.Sprintf("%s:%d", id, n+2) {
			t.Fatalf("event: %+v", event)
		}
		if name == "step.delta" && !bytes.Contains(event.Data, []byte(`"mime_type":"video/mp4"`)) {
			t.Fatalf("missing real video: %s", event.Data)
		}
	}
	consume(resumed)
	// Then repeated retrieval/cursor replay does not submit, and a new explicit
	// sync POST uses the completed receipt's exact upstream c/r/rc metadata.
	completed := call("GET", getPath, "", "caller-a")
	if body := consume(completed); completed.StatusCode != 200 || !bytes.Contains(body, []byte(`"completed"`)) {
		t.Fatalf("completed %d: %s", completed.StatusCode, body)
	}
	replay := call("GET", getPath+"?stream=true&last_event_id="+id+":3", "", "caller-a")
	replayScanner := bufio.NewScanner(replay.Body)
	for _, name := range []string{"step.stop", "interaction.completed", "done"} {
		if event := readOmniSSE(t, replayScanner); event.Name != name {
			t.Fatalf("replay: %+v", event)
		}
	}
	consume(replay)
	invalidCursor := call("GET", getPath+"?stream=true&last_event_id=other:3", "", "caller-a")
	if body := consume(invalidCursor); invalidCursor.StatusCode != 400 {
		t.Fatalf("foreign cursor %d: %s", invalidCursor.StatusCode, body)
	}
	headerRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+getPath+"?stream=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	headerRequest.Header.Set("Authorization", "Bearer caller-a")
	headerRequest.Header.Set("Last-Event-ID", id+":6")
	finished, err := http.DefaultClient.Do(headerRequest)
	if err != nil {
		t.Fatal(err)
	}
	if body := consume(finished); finished.StatusCode != 200 || bytes.Contains(body, []byte("event:")) || bytes.Contains(body, []byte("data:")) {
		t.Fatalf("finished replay %d: %s", finished.StatusCode, body)
	}
	followup := call("POST", "/v1beta/interactions", `{"model":"`+model+`","input":"edit","previous_interaction_id":"`+id+`"}`, "caller-a")
	if body := consume(followup); followup.StatusCode != 200 || !bytes.Contains(body, []byte(`"completed"`)) {
		t.Fatalf("sync followup %d: %s", followup.StatusCode, body)
	}
	stats, err := http.Get(client.url + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	var fields [][]json.RawMessage
	if err := json.Unmarshal(consume(stats), &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 {
		t.Fatalf("upstream submissions=%d, want initial + explicit followup only", len(fields))
	}
	var parent []any
	if err := json.Unmarshal(fields[1][2], &parent); err != nil {
		t.Fatal(err)
	}
	if len(parent) < 3 || parent[0] != "c_chat" || parent[1] != "r_1" || parent[2] != "rc_1" {
		t.Fatalf("wrong upstream chat: %#v", parent)
	}
}
