# EXECUTION CONTRACT KNOWLEDGE BASE

## OVERVIEW
Shared request/response types, transport context, and lifecycle contracts; score 9, a distinct SDK boundary rather than provider implementation code.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Change payload/options contracts | `types.go` | `Request`, `Options`, `Response`, `StreamResult` |
| Add execution metadata | `types.go` | Shared typed constants for selection/session hints |
| Intercept after auth selection | `types.go` | Interceptor request/response and `RequestTerminatedError` |
| Track transport attempts | `context.go` | WebSocket requirements and upstream-attempt marker |
| Override execution proxy | `request_proxy.go` | Context override and explicit removal |
| Bind resource ownership | `lifecycle.go` | `ExecutionLifecycle`, `BindExecutionResource` |
| Handle WebSocket replay | `websocket.go` | Typed replay-required error |
| Supply downstream frames | `websocket_input.go` | Single reader input and bound-account live check |
| Check contract behavior | `types_test.go`, `lifecycle_test.go`, `websocket_test.go` | Format fallback, close-once, replay classification |

## CONVENTIONS
- Request/options metadata carries durable execution identifiers; Go context carries transient transport state.
- `ResponseFormatOrSource(opts)` chooses explicit response format, otherwise source format.
- `StreamResult` contains initial upstream headers plus a receive-only chunk channel; terminal chunk failures use `StreamChunk.Err`.
- The after-auth interceptor runs before executor translation; upstream model and client-requested model remain separate fields.
- Interceptor headers replace matching entries; `ClearHeaders` removes entries first; non-empty returned body replaces the request body.
- `RequestTerminatedError` carries a downstream response without an upstream execution; its accessors return header/body copies.
- `BindExecutionResource(opts, closer)` closes once, including immediate cleanup if lifecycle binding fails.
- Generation is enabled when its metadata flag is absent or true; only explicit false disables generation.

## ANTI-PATTERNS
- Do not put `Options.ExecutionLifecycle` into request metadata.
- Do not apply `Options.ProxyURL` to token exchange or credential refresh; it overrides execution transport only.
- Do not use `LCPAffinitySessionIDMetadataKey` as a provider conversation or execution-session ID.
- Do not replay `WebsocketInput.Payload`; the receiving owner consumes frames from the single downstream reader.
- Do not reselect an account from a live WebSocket auth check; it may only reject further frames.
- Do not mutate interceptor input body or metadata; return requested modifications through the response contract.
- Do not collapse request-scoped errors into credential failures that trigger rotation or availability changes.

Package check from repository root: `go test ./sdk/cliproxy/executor`.
