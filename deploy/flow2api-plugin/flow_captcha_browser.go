package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// flowBrowserPage is a page on Flow's origin that loads without a Google
// session. Flow's own scripts must not run on it: on Flow's pages they replace
// grecaptcha.enterprise.execute with a wrapper that turns every token request
// not made by their code into the action extension_hijack_detected, which Flow
// refuses as PUBLIC_ERROR_UNUSUAL_ACTIVITY. Until 2026-10-20 the 404 page did
// not load that code; the Flow build released at 03:10 KST that day moved it
// into the script every page loads, so the page now opens with
// flowBrowserBlockedScripts blocked.
const flowBrowserPage = "https://flow.google.com/404"

// flowBrowserBlockedScripts are Flow's own application scripts.
var flowBrowserBlockedScripts = []string{"*/_/mss/boq-labs-ai-sandbox/*", "*://flow.google.com/_/*/_/js/*"}

const flowBrowserMessageLimit = 8 << 20

// flowBrowserCall bounds one token. Minting is credential acquisition, the one
// wait this plugin may bound by a clock.
var flowBrowserCall = 30 * time.Second

// flowBrowser mints Flow's reCAPTCHA tokens in a signed-out Chrome over the
// DevTools protocol. Flow's free low-priority queue refuses the tokens
// nativeToken asks for without a browser and accepts these. The browser holds
// no Google session, so it never rotates the account's cookies.
type flowBrowser struct {
	mu        sync.Mutex
	base      string
	page      string
	conn      net.Conn
	reader    *bufio.Reader
	seq       int
	events    map[string]bool
	userAgent string
}

func (browser *flowBrowser) token(ctx context.Context, client *http.Client, base, action string) (flowCaptchaToken, error) {
	browser.mu.Lock()
	defer browser.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, flowBrowserCall)
	defer cancel()
	if browser.base != base {
		browser.closeLocked(client)
		browser.base = base
	}
	if browser.conn == nil {
		if err := browser.openLocked(ctx, client); err != nil {
			browser.closeLocked(client)
			return flowCaptchaToken{}, err
		}
	} else if err := browser.deadlineLocked(ctx); err != nil {
		browser.closeLocked(client)
		return flowCaptchaToken{}, failure(503, "flow_captcha_browser_failed")
	}
	var token string
	execute := "grecaptcha.enterprise.execute(" + strconv.Quote(flowRecaptchaKey) + ", {action: " + strconv.Quote(action) + "})"
	if err := browser.evaluateLocked(execute, &token); err != nil {
		browser.closeLocked(client)
		return flowCaptchaToken{}, err
	}
	return flowCaptchaToken{value: token, userAgent: browser.userAgent}, nil
}

func (browser *flowBrowser) close(client *http.Client) {
	browser.mu.Lock()
	defer browser.mu.Unlock()
	browser.closeLocked(client)
}

func (browser *flowBrowser) openLocked(ctx context.Context, client *http.Client) error {
	endpoint, err := url.Parse(browser.base)
	if err != nil || endpoint.Host == "" {
		return failure(503, "flow_captcha_browser_unreachable")
	}
	browser.closeLeftoverPagesLocked(ctx, client)
	var page struct {
		ID string `json:"id"`
	}
	if err := browser.devtools(ctx, client, http.MethodPut, "/json/new?about:blank", &page); err != nil || page.ID == "" {
		return failure(503, "flow_captcha_browser_unreachable")
	}
	browser.page = page.ID
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", endpoint.Host)
	if err != nil {
		return failure(503, "flow_captcha_browser_unreachable")
	}
	browser.conn, browser.reader = conn, bufio.NewReader(conn)
	if err := browser.deadlineLocked(ctx); err != nil {
		return failure(503, "flow_captcha_browser_unreachable")
	}
	if err := browser.upgradeLocked("/devtools/page/" + page.ID); err != nil {
		return err
	}
	browser.events = map[string]bool{}
	if _, err := browser.callLocked("Page.enable", nil); err != nil {
		return err
	}
	// The page's policy must not refuse the reCAPTCHA script.
	if _, err := browser.callLocked("Page.setBypassCSP", map[string]any{"enabled": true}); err != nil {
		return err
	}
	if _, err := browser.callLocked("Network.enable", nil); err != nil {
		return err
	}
	// Every token page starts from an empty cookie jar. Flow refuses the free
	// low-priority queue as UNUSUAL_ACTIVITY_TOO_MUCH_TRAFFIC once one
	// reCAPTCHA client (its _GRECAPTCHA cookie) has minted many tokens, and the
	// signed-out browser holds no session a cleared jar could lose.
	if _, err := browser.callLocked("Network.clearBrowserCookies", nil); err != nil {
		return err
	}
	if _, err := browser.callLocked("Network.setBlockedURLs", map[string]any{"urls": flowBrowserBlockedScripts}); err != nil {
		return err
	}
	if _, err := browser.callLocked("Page.navigate", map[string]any{"url": flowBrowserPage}); err != nil {
		return err
	}
	if err := browser.awaitLocked("Page.loadEventFired"); err != nil {
		return err
	}
	loader := `new Promise((resolve, reject) => {
	if (location.origin !== "https://flow.google.com") {
		reject("page origin " + location.origin);
		return;
	}
	const script = document.createElement("script");
	script.src = "https://www.google.com/recaptcha/enterprise.js?render=` + flowRecaptchaKey + `";
	script.onerror = () => reject("reCAPTCHA script failed to load");
	script.onload = () => grecaptcha.enterprise.ready(() => String(grecaptcha.enterprise.execute).includes("extension_hijack_detected")
		? reject("Flow's page wrapped grecaptcha.enterprise.execute")
		: resolve(navigator.userAgent));
	(document.head || document.documentElement).appendChild(script);
})`
	return browser.evaluateLocked(loader, &browser.userAgent)
}

// closeLeftoverPagesLocked closes the token pages an instance that stopped
// without closing its page left behind; each keeps a reCAPTCHA client running
// in the same browser. A failed listing leaves them for the next open.
func (browser *flowBrowser) closeLeftoverPagesLocked(ctx context.Context, client *http.Client) {
	var targets []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	if err := browser.devtools(ctx, client, http.MethodGet, "/json/list", &targets); err != nil {
		return
	}
	for _, target := range targets {
		if target.Type != "page" || target.URL != flowBrowserPage {
			continue
		}
		if err := browser.devtools(ctx, client, http.MethodGet, "/json/close/"+target.ID, nil); err != nil {
			_ = err // A page left open only holds browser memory until Chrome restarts.
		}
	}
}

func (browser *flowBrowser) closeLocked(client *http.Client) {
	if browser.conn != nil {
		if err := browser.conn.Close(); err != nil {
			_ = err // The connection is dropped either way.
		}
		browser.conn, browser.reader = nil, nil
	}
	if browser.page != "" {
		ctx, cancel := context.WithTimeout(context.Background(), flowBrowserCall)
		if err := browser.devtools(ctx, client, http.MethodGet, "/json/close/"+browser.page, nil); err != nil {
			_ = err // A page left open only holds browser memory until Chrome restarts.
		}
		cancel()
		browser.page = ""
	}
	browser.userAgent, browser.events = "", nil
}

func (browser *flowBrowser) deadlineLocked(ctx context.Context) error {
	deadline, _ := ctx.Deadline()
	return browser.conn.SetDeadline(deadline)
}

// devtools calls Chrome's DevTools HTTP endpoint. Chrome answers only a Host
// header that is an IP address, and the browser is addressed by its container
// name, so the header is set explicitly.
func (browser *flowBrowser) devtools(ctx context.Context, client *http.Client, method, path string, target any) error {
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(browser.base, "/")+path, nil)
	if err != nil {
		return err
	}
	request.Host = "127.0.0.1"
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()
	if response.StatusCode != http.StatusOK {
		return errors.New("devtools " + path + ": " + response.Status)
	}
	if target == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(target)
}

func (browser *flowBrowser) upgradeLocked(path string) error {
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return failure(500, "flow_captcha_browser_failed")
	}
	handshake := "GET " + path + " HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(key) + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := io.WriteString(browser.conn, handshake); err != nil {
		return failure(503, "flow_captcha_browser_unreachable")
	}
	response, err := http.ReadResponse(browser.reader, nil)
	if err != nil || response.StatusCode != http.StatusSwitchingProtocols {
		return failure(503, "flow_captcha_browser_unreachable")
	}
	return nil
}

// callLocked sends one DevTools command and reads until its answer, noting the
// events that arrive in between.
func (browser *flowBrowser) callLocked(method string, params any) (json.RawMessage, error) {
	browser.seq++
	command := map[string]any{"id": browser.seq, "method": method}
	if params != nil {
		command["params"] = params
	}
	payload, err := json.Marshal(command)
	if err != nil {
		return nil, failure(500, "flow_captcha_browser_failed")
	}
	if err := browser.writeLocked(payload); err != nil {
		return nil, failure(503, "flow_captcha_browser_failed")
	}
	for {
		var reply struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		raw, err := browser.readLocked()
		if err != nil || json.Unmarshal(raw, &reply) != nil {
			return nil, failure(503, "flow_captcha_browser_failed")
		}
		switch {
		case reply.Method != "":
			browser.events[reply.Method] = true
		case reply.ID != browser.seq:
		case len(reply.Error) > 0:
			return nil, failure(503, "flow_captcha_browser_failed")
		default:
			return reply.Result, nil
		}
	}
}

func (browser *flowBrowser) awaitLocked(event string) error {
	for !browser.events[event] {
		var message struct {
			Method string `json:"method"`
		}
		raw, err := browser.readLocked()
		if err != nil || json.Unmarshal(raw, &message) != nil {
			return failure(503, "flow_captcha_browser_failed")
		}
		if message.Method != "" {
			browser.events[message.Method] = true
		}
	}
	return nil
}

func (browser *flowBrowser) evaluateLocked(expression string, target *string) error {
	result, err := browser.callLocked("Runtime.evaluate", map[string]any{"expression": expression, "awaitPromise": true, "returnByValue": true})
	if err != nil {
		return err
	}
	var evaluated struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
		ExceptionDetails json.RawMessage `json:"exceptionDetails"`
	}
	if json.Unmarshal(result, &evaluated) != nil || len(evaluated.ExceptionDetails) > 0 {
		return failure(503, "flow_captcha_browser_failed")
	}
	value, ok := evaluated.Result.Value.(string)
	if !ok || value == "" {
		return failure(503, "flow_captcha_browser_failed")
	}
	*target = value
	return nil
}

// writeLocked sends one text frame. A client masks every frame it sends.
func (browser *flowBrowser) writeLocked(payload []byte) error {
	frame := []byte{0x81}
	switch size := len(payload); {
	case size < 126:
		frame = append(frame, 0x80|byte(size))
	case size <= 0xFFFF:
		frame = binary.BigEndian.AppendUint16(append(frame, 0x80|126), uint16(size))
	default:
		frame = binary.BigEndian.AppendUint64(append(frame, 0x80|127), uint64(size))
	}
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	frame = append(frame, mask...)
	for i, b := range payload {
		frame = append(frame, b^mask[i%4])
	}
	_, err := browser.conn.Write(frame)
	return err
}

// readLocked reads one message, joining its fragments. Chrome sends no pings.
func (browser *flowBrowser) readLocked() ([]byte, error) {
	var message []byte
	for {
		var head [2]byte
		if _, err := io.ReadFull(browser.reader, head[:]); err != nil {
			return nil, err
		}
		size := uint64(head[1] & 0x7F)
		switch size {
		case 126:
			var extended [2]byte
			if _, err := io.ReadFull(browser.reader, extended[:]); err != nil {
				return nil, err
			}
			size = uint64(binary.BigEndian.Uint16(extended[:]))
		case 127:
			var extended [8]byte
			if _, err := io.ReadFull(browser.reader, extended[:]); err != nil {
				return nil, err
			}
			size = binary.BigEndian.Uint64(extended[:])
		}
		var mask [4]byte
		masked := head[1]&0x80 != 0
		if masked {
			if _, err := io.ReadFull(browser.reader, mask[:]); err != nil {
				return nil, err
			}
		}
		if size > flowBrowserMessageLimit-uint64(len(message)) {
			return nil, errors.New("devtools message too large")
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(browser.reader, payload); err != nil {
			return nil, err
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		switch head[0] & 0x0F {
		case 0x8:
			return nil, io.EOF
		case 0x9, 0xA:
			continue
		}
		message = append(message, payload...)
		if head[0]&0x80 != 0 {
			return message, nil
		}
	}
}
