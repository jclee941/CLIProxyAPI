# API SERVER KNOWLEDGE BASE

## OVERVIEW
Gin server assembly, protocol multiplexing, and route lifecycle; score 14, a distinct transport domain.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Construct/start/stop server | `server.go` | `NewServer`, `Handler`, `Start`, `Stop` |
| Embedding options | `server_options.go` | Middleware, router, plugin host, auth hooks |
| Provider routes and model lists | `server_routes.go` | Compatibility endpoints and OAuth redirects |
| Management route installation | `server_management.go` | Lazy registration and availability gate |
| Authentication and safe mode | `server_middleware.go` | Access checks and example-key restrictions |
| Native plugin fallback | `server_frontend_http.go` | Gin NoRoute dispatch |
| Runtime configuration update | `server_reload.go` | `UpdateClientsContext` and old YAML snapshot |
| HTTP/RESP discrimination | `protocol_multiplexer.go` | Per-connection routing |
| Listener lifecycle | `mux_listener.go`, `buffered_conn.go` | Preserve peeked bytes and shutdown behavior |
| Redis-compatible usage interface | `redis_queue_protocol.go` | Management-key authentication |
| Heartbeat endpoint | `server_keepalive.go` | Optional embedding watchdog |
| Management implementation | `handlers/AGENTS.md` | Handler state and persistence rules |
| Request-log capture | `middleware/AGENTS.md` | Streaming and deferred-body rules |
| Native integration fixture | `testdata/frontend_http.c` | Used by frontend HTTP native tests |

## CONVENTIONS
- Server receiver methods are split by responsibility across `server_*.go`.
- Public compatibility handlers are assembled here; implementations live under SDK handlers.
- Management route registration and route availability are separate atomic states.
- Keep `/backend-api/codex` aliases aligned with the corresponding response endpoints.
- Config reload compares against a serialized prior snapshot, not a shared mutable pointer.
- HTTP and RESP share the listening socket; protocol inspection belongs to each accepted connection.
- `Stop` immediately closes listeners and HTTP connections; it deliberately does not drain streams.
- NoRoute dispatch checks plugin resources, then management paths, then frontend fallback; registered routes retain precedence.
- Control-panel asset bootstrap uses a detached context so client disconnects do not cancel installation.
- Use `go test ./internal/api/...` for this subtree's routing and transport checks.

## ANTI-PATTERNS
- Do not perform TLS handshakes or blocking byte inspection inside the accept loop.
- Do not discard bytes peeked while selecting the HTTP/RESP handler.
- Do not equate registered management routes with management being enabled.
- Do not register the same management routes again during config reload.
- Do not remove Codex direct-route aliases when changing their shared handlers.
- Do not treat a stale shared config pointer as the previous reload state.
- Do not invoke global access authentication for scoped bearer frontend routes, even on failure.
- Do not permit an empty caller scope for frontend plugin resources using core authentication.
- Do not send `codexAlphaSearch` payloads through protocol translators; they already use Codex search format.
