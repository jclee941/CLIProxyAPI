package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const cdpMessageLimit = 2 << 20

type cdpCredentialSource struct {
	origin string
	client *http.Client
	dialer *websocket.Dialer
}

func newCDPCredentialSource() credentialSource {
	return &cdpCredentialSource{
		origin: profileCDPOrigin,
		client: &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}},
		dialer: &websocket.Dialer{HandshakeTimeout: 20 * time.Second},
	}
}

type cdpParams struct {
	TargetID          string   `json:"targetId,omitempty"`
	SessionID         string   `json:"sessionId,omitempty"`
	Flatten           bool     `json:"flatten,omitempty"`
	Expression        string   `json:"expression,omitempty"`
	ReturnByValue     bool     `json:"returnByValue,omitempty"`
	Silent            bool     `json:"silent,omitempty"`
	ThrowOnSideEffect bool     `json:"throwOnSideEffect,omitempty"`
	URLs              []string `json:"urls,omitempty"`
}

type cdpCommand struct {
	ID        uint64    `json:"id"`
	Method    string    `json:"method"`
	SessionID string    `json:"sessionId,omitempty"`
	Params    cdpParams `json:"params"`
}

type cdpWire struct {
	ctx              context.Context
	connection       *websocket.Conn
	nextID           uint64
	sessions         []string
	closeSocket      func() error
	stopCancellation func() bool
	cancellationDone chan struct{}
}

func (source *cdpCredentialSource) connect(ctx context.Context) (*cdpWire, error) {
	address, err := source.discover(ctx)
	if err != nil {
		return nil, err
	}
	connection, response, err := source.dialer.DialContext(ctx, address, nil)
	if response != nil && response.Body != nil {
		if errClose := response.Body.Close(); errClose != nil && err == nil {
			err = errClose
		}
	}
	if err != nil {
		if connection != nil {
			connection.Close()
		}
		return nil, failure(503, "source_unavailable")
	}
	wire := &cdpWire{ctx: ctx, connection: connection, closeSocket: sync.OnceValue(connection.Close), cancellationDone: make(chan struct{})}
	wire.stopCancellation = context.AfterFunc(ctx, func() { wire.closeSocket(); close(wire.cancellationDone) })
	connection.SetReadLimit(cdpMessageLimit)
	deadline, _ := ctx.Deadline()
	if err := connection.SetReadDeadline(deadline); err != nil {
		wire.close()
		return nil, failure(502, "source_protocol_failed")
	}
	if err := connection.SetWriteDeadline(deadline); err != nil {
		wire.close()
		return nil, failure(502, "source_protocol_failed")
	}
	return wire, nil
}

func (source *cdpCredentialSource) discover(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.origin+"/json/version", nil)
	if err != nil {
		return "", failure(503, "source_unavailable")
	}
	client := *source.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return "", failure(503, "source_unavailable")
	}
	body, errRead := io.ReadAll(io.LimitReader(response.Body, cdpMessageLimit+1))
	errClose := response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", failure(503, "source_unavailable")
	}
	if errRead != nil || errClose != nil || len(body) > cdpMessageLimit {
		return "", failure(502, "source_protocol_failed")
	}
	var version struct {
		WebSocketURL string `json:"webSocketDebuggerUrl"`
	}
	if json.Unmarshal(body, &version) != nil {
		return "", failure(502, "source_protocol_failed")
	}
	address, err := url.Parse(version.WebSocketURL)
	origin, errOrigin := url.Parse(source.origin)
	if err != nil || errOrigin != nil || address.Scheme != "ws" || address.Host != origin.Host || address.User != nil || address.RawQuery != "" || address.Fragment != "" || !strings.HasPrefix(address.Path, "/devtools/browser/") || strings.TrimPrefix(address.Path, "/devtools/browser/") == "" {
		return "", failure(503, "source_unavailable")
	}
	return address.String(), nil
}

func cdpCall[Result any](wire *cdpWire, command cdpCommand) (Result, error) {
	var result Result
	if wire.ctx.Err() != nil {
		return result, failure(499, "source_cancelled")
	}
	wire.nextID++
	command.ID = wire.nextID
	if err := wire.connection.WriteJSON(command); err != nil {
		return result, failure(502, "source_protocol_failed")
	}
	for {
		messageType, message, err := wire.connection.ReadMessage()
		if err != nil || messageType != websocket.TextMessage {
			return result, failure(502, "source_protocol_failed")
		}
		var reply struct {
			ID        uint64          `json:"id"`
			SessionID string          `json:"sessionId"`
			Method    string          `json:"method"`
			Result    json.RawMessage `json:"result"`
			Error     json.RawMessage `json:"error"`
		}
		if json.Unmarshal(message, &reply) != nil {
			return result, failure(502, "source_protocol_failed")
		}
		if reply.ID == 0 && reply.Method != "" {
			continue
		}
		if reply.ID != command.ID || reply.SessionID != command.SessionID || len(reply.Error) != 0 || len(reply.Result) == 0 || string(reply.Result) == "null" || json.Unmarshal(reply.Result, &result) != nil {
			return result, failure(502, "source_protocol_failed")
		}
		return result, nil
	}
}

func (wire *cdpWire) close() error {
	var cleanupError error
	for _, session := range wire.sessions {
		if _, err := cdpCall[struct{}](wire, cdpCommand{Method: "Target.detachFromTarget", Params: cdpParams{SessionID: session}}); err != nil {
			cleanupError = err
		}
	}
	if err := wire.closeSocket(); err != nil {
		cleanupError = failure(502, "source_protocol_failed")
	}
	if !wire.stopCancellation() {
		<-wire.cancellationDone
	}
	return cleanupError
}
