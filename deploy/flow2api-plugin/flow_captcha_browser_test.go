package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

const devtoolsFixtureAgent = "Fixture Chrome/155.0.0.0"

var errDevtoolsFrame = errors.New("not a final masked text frame")

type devtoolsFixture struct {
	t           *testing.T
	server      *httptest.Server
	mu          sync.Mutex
	pages       int
	stale       []string
	closed      []string
	methods     []string
	blocked     []string
	expressions []string
}

func newDevtoolsFixture(t *testing.T) *devtoolsFixture {
	t.Helper()
	fixture := &devtoolsFixture{t: t}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serve))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (fixture *devtoolsFixture) serve(writer http.ResponseWriter, request *http.Request) {
	if request.Host != "127.0.0.1" {
		fixture.t.Errorf("DevTools Host = %q, but Chrome answers only an IP address", request.Host)
	}
	fixture.mu.Lock()
	switch {
	case request.Method == http.MethodPut && request.URL.Path == "/json/new":
		fixture.pages++
		page := fmt.Sprintf("page-%d", fixture.pages)
		fixture.mu.Unlock()
		writeFixture(fixture.t, writer, `{"id":"`+page+`","webSocketDebuggerUrl":"ws://127.0.0.1/devtools/page/`+page+`"}`)
	case strings.HasPrefix(request.URL.Path, "/json/close/"):
		fixture.closed = append(fixture.closed, strings.TrimPrefix(request.URL.Path, "/json/close/"))
		fixture.mu.Unlock()
		writeFixture(fixture.t, writer, "Target is closing")
	case request.Method == http.MethodGet && request.URL.Path == "/json/list":
		targets := []map[string]string{}
		open := slices.Clone(fixture.stale)
		for page := 1; page <= fixture.pages; page++ {
			open = append(open, fmt.Sprintf("page-%d", page))
		}
		for _, id := range open {
			if !slices.Contains(fixture.closed, id) {
				targets = append(targets, map[string]string{"id": id, "type": "page", "url": flowBrowserPage})
			}
		}
		fixture.mu.Unlock()
		listing, err := json.Marshal(targets)
		if err != nil {
			fixture.t.Error(err)
		}
		writeFixture(fixture.t, writer, string(listing))
	case strings.HasPrefix(request.URL.Path, "/devtools/page/"):
		fixture.mu.Unlock()
		fixture.socket(writer, request, strings.TrimPrefix(request.URL.Path, "/devtools/page/"))
	default:
		fixture.mu.Unlock()
		fixture.t.Errorf("unexpected DevTools request %s %s", request.Method, request.URL)
		writer.WriteHeader(http.StatusNotFound)
	}
}

func (fixture *devtoolsFixture) socket(writer http.ResponseWriter, request *http.Request, page string) {
	t := fixture.t
	accept := sha1.Sum([]byte(request.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	connection, buffered, err := writer.(http.Hijacker).Hijack()
	if err != nil {
		t.Error(err)
		return
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	}()
	send := func(message string) bool {
		frame := []byte{0x81}
		if len(message) < 126 {
			frame = append(frame, byte(len(message)))
		} else {
			frame = binary.BigEndian.AppendUint16(append(frame, 126), uint16(len(message)))
		}
		if _, err := buffered.Write(append(frame, message...)); err != nil {
			return false
		}
		return buffered.Flush() == nil
	}
	if _, err := buffered.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(accept[:]) + "\r\n\r\n"); err != nil || buffered.Flush() != nil {
		return
	}
	tokens := 0
	for {
		raw, err := readClientFrame(buffered.Reader)
		if errors.Is(err, errDevtoolsFrame) {
			t.Error(err)
		}
		if err != nil {
			return
		}
		var command struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				URL        string   `json:"url"`
				Expression string   `json:"expression"`
				Name       string   `json:"name"`
				URLs       []string `json:"urls"`
			} `json:"params"`
		}
		if err := json.Unmarshal(raw, &command); err != nil {
			t.Error(err)
			return
		}
		result := map[string]any{}
		fixture.mu.Lock()
		fixture.methods = append(fixture.methods, command.Method)
		fixture.mu.Unlock()
		switch command.Method {
		case "Page.enable", "Page.setBypassCSP", "Network.enable", "Network.clearBrowserCookies":
		case "Network.setBlockedURLs":
			fixture.mu.Lock()
			fixture.blocked = append(fixture.blocked, command.Params.URLs...)
			fixture.mu.Unlock()
		case "Page.navigate":
			if command.Params.URL != flowBrowserPage {
				t.Errorf("navigated to %q", command.Params.URL)
			}
			if !send(fmt.Sprintf(`{"id":%d,"result":{"frameId":"frame"}}`, command.ID)) || !send(`{"method":"Page.loadEventFired","params":{"timestamp":1}}`) {
				return
			}
			continue
		case "Runtime.evaluate":
			value := devtoolsFixtureAgent
			switch expression := command.Params.Expression; {
			case strings.Contains(expression, "grecaptcha.enterprise.execute("):
				tokens++
				value = fmt.Sprintf("browser-token-%s-%d", page, tokens)
				fixture.mu.Lock()
				fixture.expressions = append(fixture.expressions, expression)
				fixture.mu.Unlock()
			case !strings.Contains(expression, "enterprise.js?render="+flowRecaptchaKey):
				t.Errorf("unexpected expression %q", expression)
			}
			result["result"] = map[string]any{"type": "string", "value": value}
		default:
			t.Errorf("unexpected DevTools method %s", command.Method)
		}
		reply, err := json.Marshal(map[string]any{"id": command.ID, "result": result})
		if err != nil || !send(string(reply)) {
			return
		}
	}
}

func readClientFrame(reader *bufio.Reader) ([]byte, error) {
	var head [2]byte
	if _, err := io.ReadFull(reader, head[:]); err != nil {
		return nil, err
	}
	if head[0] != 0x81 || head[1]&0x80 == 0 {
		return nil, fmt.Errorf("%w: header %x", errDevtoolsFrame, head)
	}
	size := int(head[1] & 0x7F)
	switch size {
	case 126:
		var extended [2]byte
		if _, err := io.ReadFull(reader, extended[:]); err != nil {
			return nil, err
		}
		size = int(binary.BigEndian.Uint16(extended[:]))
	case 127:
		return nil, fmt.Errorf("%w: 64-bit length", errDevtoolsFrame)
	}
	var mask [4]byte
	if _, err := io.ReadFull(reader, mask[:]); err != nil {
		return nil, err
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return payload, nil
}

func TestFlowBrowserTokensCarryTheBrowserIntoTheSubmission(t *testing.T) {
	// Given: a signed-out browser minting tokens, and Flow refusing the first
	// low-priority token before accepting the next.
	fixture := newFlowFixture(t)
	devtools := newDevtoolsFixture(t)
	media := []any{flowTestOp, flowTestProject, flowTestMedia}
	refused := flowErrorEnvelope(t, []any{"wrb.fr", "YhhmEf", nil, nil, nil, []any{3, nil, []any{[]any{"type.googleapis.com/google.rpc.ErrorInfo", []any{"PUBLIC_ERROR_UNUSUAL_ACTIVITY"}}}}, "generic"})
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("YhhmEf", refused, rpcEnvelope(t, "YhhmEf", []any{[]any{media}}))
	fixture.reply("jwpduf", rpcEnvelope(t, "jwpduf", []any{[]any{append(slices.Clone(media), fixture.link("video"))}}))
	service, record := flowService(t, fixture)
	service.config.FlowCaptchaProvider, service.config.FlowCaptchaBaseURL = "browser", devtools.server.URL
	t.Cleanup(func() { service.captchaBrowser.close(service.client) })

	// When: a low-priority video is requested.
	mime, _ := flowInlineMedia(t, flowExecute(t, service, record, "flow-veo-3.1-lite", `{"contents":[{"parts":[{"text":"a fox"}]}],"generationConfig":{"durationSeconds":8,"flow":{"priority":"low"}}}`))

	// Then: each submission carries a browser token under the browser's user
	// agent, the refused token's page is replaced, and no native token is minted.
	if mime != "video/mp4" || fixture.count("YhhmEf") != 2 {
		t.Fatalf("video %s after %d submissions", mime, fixture.count("YhhmEf"))
	}
	fixture.mu.Lock()
	requests := []string{fixture.calls["YhhmEf"][0].Get("f.req"), fixture.calls["YhhmEf"][1].Get("f.req")}
	agents := slices.Clone(fixture.agents["YhhmEf"])
	native := fixture.reload
	fixture.mu.Unlock()
	if !strings.Contains(requests[0], `\"browser-token-page-1-1\"`) || !strings.Contains(requests[1], `\"browser-token-page-2-1\"`) {
		t.Fatalf("submissions did not carry the browser tokens in order")
	}
	if !slices.Equal(agents, []string{devtoolsFixtureAgent, devtoolsFixtureAgent}) || native != nil {
		t.Fatalf("agents %q, native reload %v", agents, native)
	}
	devtools.mu.Lock()
	defer devtools.mu.Unlock()
	if devtools.pages != 2 || !slices.Equal(devtools.closed, []string{"page-1"}) || len(devtools.expressions) != 2 || !strings.Contains(devtools.expressions[0], `{action: "VIDEO_GENERATION"}`) {
		t.Fatalf("pages %d, closed %v, expressions %q", devtools.pages, devtools.closed, devtools.expressions)
	}
}

func TestFlowBrowserOutageFallsBackToNativeTokens(t *testing.T) {
	// Given: the token browser is unreachable.
	fixture := newFlowFixture(t)
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("ogiZ0b", rpcEnvelope(t, "ogiZ0b", []any{[]any{[]any{flowTestMedia, fixture.link("image")}}}))
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	service, record := flowService(t, fixture)
	service.config.FlowCaptchaProvider, service.config.FlowCaptchaBaseURL = "browser", gone.URL

	// When: an image is requested.
	flowInlineMedia(t, flowExecute(t, service, record, "flow-nano-banana-2", `{"contents":[{"parts":[{"text":"an apple"}]}]}`))

	// Then: the account's native token carried the submission.
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.reload.Get("sa") != "IMAGE_GENERATION" || !slices.Equal(fixture.agents["ogiZ0b"], []string{flowUserAgent}) {
		t.Fatalf("native reload %v, agents %q", fixture.reload, fixture.agents["ogiZ0b"])
	}
}

func TestFlowBrowserBlocksFlowScriptsBeforeOpeningThePage(t *testing.T) {
	// Given: a browser minting tokens on Flow's origin.
	fixture := newFlowFixture(t)
	devtools := newDevtoolsFixture(t)
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("ogiZ0b", rpcEnvelope(t, "ogiZ0b", []any{[]any{[]any{flowTestMedia, fixture.link("image")}}}))
	service, record := flowService(t, fixture)
	service.config.FlowCaptchaProvider, service.config.FlowCaptchaBaseURL = "browser", devtools.server.URL
	t.Cleanup(func() { service.captchaBrowser.close(service.client) })

	// When: an image is requested.
	flowInlineMedia(t, flowExecute(t, service, record, "flow-nano-banana-2", `{"contents":[{"parts":[{"text":"a fox"}]}]}`))

	// Then: Flow's own scripts, which wrap reCAPTCHA on Flow's pages, were
	// blocked before the page was opened.
	devtools.mu.Lock()
	defer devtools.mu.Unlock()
	blockedAt, openedAt := slices.Index(devtools.methods, "Network.setBlockedURLs"), slices.Index(devtools.methods, "Page.navigate")
	if blockedAt < 0 || openedAt < blockedAt || !slices.Equal(devtools.blocked, flowBrowserBlockedScripts) {
		t.Fatalf("methods %q, blocked %q", devtools.methods, devtools.blocked)
	}
}

func TestFlowBrowserOpensEachTokenPageWithoutLeftoversOrCookies(t *testing.T) {
	// Given: token pages an earlier instance left open in the browser.
	fixture := newFlowFixture(t)
	devtools := newDevtoolsFixture(t)
	devtools.stale = []string{"left-1", "left-2"}
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("ogiZ0b", rpcEnvelope(t, "ogiZ0b", []any{[]any{[]any{flowTestMedia, fixture.link("image")}}}))
	service, record := flowService(t, fixture)
	service.config.FlowCaptchaProvider, service.config.FlowCaptchaBaseURL = "browser", devtools.server.URL
	t.Cleanup(func() { service.captchaBrowser.close(service.client) })

	// When: an image is requested.
	flowInlineMedia(t, flowExecute(t, service, record, "flow-nano-banana-2", `{"contents":[{"parts":[{"text":"a fox"}]}]}`))

	// Then: the leftover pages were closed before one new page opened, and the
	// new page emptied the cookie jar before navigating to the token page.
	devtools.mu.Lock()
	defer devtools.mu.Unlock()
	clearedAt, openedAt := slices.Index(devtools.methods, "Network.clearBrowserCookies"), slices.Index(devtools.methods, "Page.navigate")
	if !slices.Equal(devtools.closed, []string{"left-1", "left-2"}) || devtools.pages != 1 || clearedAt < 0 || openedAt < clearedAt {
		t.Fatalf("closed %v, pages %d, methods %q", devtools.closed, devtools.pages, devtools.methods)
	}
}

func TestFlowBrowserReplacesATokenFlowThrottledForUnusualTraffic(t *testing.T) {
	// Given: Flow throttling the first low-priority token's client as unusual
	// traffic before accepting a token from a fresh page.
	fixture := newFlowFixture(t)
	devtools := newDevtoolsFixture(t)
	media := []any{flowTestOp, flowTestProject, flowTestMedia}
	throttled := flowErrorEnvelope(t, []any{"wrb.fr", "YhhmEf", nil, nil, nil, []any{3, nil, []any{[]any{"type.googleapis.com/google.rpc.ErrorInfo", []any{"PUBLIC_ERROR_UNUSUAL_ACTIVITY_TOO_MUCH_TRAFFIC"}}}}, "generic"})
	fixture.reply("jHPbke", rpcEnvelope(t, "jHPbke", []any{flowTestProject}))
	fixture.reply("YhhmEf", throttled, rpcEnvelope(t, "YhhmEf", []any{[]any{media}}))
	fixture.reply("jwpduf", rpcEnvelope(t, "jwpduf", []any{[]any{append(slices.Clone(media), fixture.link("video"))}}))
	service, record := flowService(t, fixture)
	service.config.FlowCaptchaProvider, service.config.FlowCaptchaBaseURL = "browser", devtools.server.URL
	t.Cleanup(func() { service.captchaBrowser.close(service.client) })

	// When: a low-priority video is requested.
	mime, _ := flowInlineMedia(t, flowExecute(t, service, record, "flow-veo-3.1-lite", `{"contents":[{"parts":[{"text":"a fox"}]}],"generationConfig":{"durationSeconds":8,"flow":{"priority":"low"}}}`))

	// Then: the throttled token's page was replaced, the resent submission
	// carried the fresh page's token, and the video was delivered.
	if mime != "video/mp4" || fixture.count("YhhmEf") != 2 {
		t.Fatalf("video %s after %d submissions", mime, fixture.count("YhhmEf"))
	}
	fixture.mu.Lock()
	resent := fixture.calls["YhhmEf"][1].Get("f.req")
	fixture.mu.Unlock()
	if !strings.Contains(resent, `\"browser-token-page-2-1\"`) {
		t.Fatalf("the resent submission did not carry the fresh page's token")
	}
	devtools.mu.Lock()
	defer devtools.mu.Unlock()
	if devtools.pages != 2 || !slices.Equal(devtools.closed, []string{"page-1"}) {
		t.Fatalf("pages %d, closed %v", devtools.pages, devtools.closed)
	}
}
