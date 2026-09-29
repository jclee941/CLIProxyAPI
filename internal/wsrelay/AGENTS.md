# HTTP-OVER-WEBSOCKET RELAY

## OVERVIEW
Provider session routing and correlated HTTP/stream transport; score 8, a distinct concurrent wire-protocol domain.

## WHERE TO LOOK
| Task | File |
|------|------|
| Session table, upgrade handler, shutdown | `manager.go` |
| HTTP request/response adaptation | `http.go` |
| JSON envelope and message types | `message.go` |
| Pending calls, read/write loop, cancellation | `session.go` |
| Backpressure and cleanup regressions | `session_test.go` |

## CONVENTIONS
- `Manager.Send` routes a typed message to a connected provider session.
- Correlation IDs connect HTTP requests with response or streaming envelopes.
- `NonStream` and `Stream` expose distinct higher-level response contracts.
- Pending requests use bounded delivery channels.
- Context cancellation installs request cleanup through `context.AfterFunc`.
- Sending the wire request marks the upstream attempt for SDK accounting.
- Slow consumers must not stop the session from processing other pending requests.
- Session liveness follows the root timeout-policy exception for this package.
- The AIStudio executor is the main HTTP adaptation consumer.

## ANTI-PATTERNS
- Do not leave canceled request IDs registered in the pending map.
- Do not lose a terminal error merely because a consumer buffer is full.
- Do not turn slow-consumer handling into a blocking session-wide send.
- Do not close delivery channels concurrently without the pending-request ownership protocol.
- Do not mark an upstream attempt before the relay reaches its actual send path.

Run `go test ./internal/wsrelay` for relay behavior.
Use the race detector for delivery/cancellation ownership changes.
Executor integration cases also live in the AIStudio executor tests.
