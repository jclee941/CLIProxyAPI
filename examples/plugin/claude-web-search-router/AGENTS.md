# CLAUDE WEB SEARCH ROUTER PLUGIN

## OVERVIEW
Go-only Claude Code `web_search` router/executor across antigravity, codex, xai, and Tavily; score 13: 22 files, >70% code, module/config boundary, >30 LSP symbols, and 30 references to `routeBackend`.

## WHERE TO LOOK
| Task | Location | Detail |
| --- | --- | --- |
| ABI entry, config, registration | `go/main.go` | `handleMethod`, `configure`, `pluginRegistration`, `routeModel`, `execute` |
| Request detection | `go/detect.go` | `web_search_20250305`/`web_search_20260209`, Claude Code heuristics, query + `max_uses` extraction |
| Route selection | `go/fallback.go` | `defaultWebSearchFallbackChain`, `tryRouteBackend`, `routeWithExecutionOrchestration` |
| Execution retry across backends | `go/execution_fallback.go` | `buildExecutionPlans*`, `runOrderedExecutionPlans` |
| Streaming path | `go/execute_stream.go`, `go/stream_forward.go` | Plugin stream emit/close, host model stream forwarding |
| Backend penalties | `go/penalty.go` | In-memory scores: +5 on failure, -1 on success, stable sort |
| Target model names | `go/model_resolve.go` | Antigravity via registry lookup; Codex/xAI defaults |
| Tavily backend | `go/tavily.go` | Key round-robin, hits -> Claude blocks |
| Claude reply synthesis | `go/claude_response.go` | `claudeStreamBuilder` SSE and message JSON |
| Config fields and routes | `README.md` | `route` values, `plugins.configs.claude-web-search-router` |

## CONVENTIONS
- Default `route` is `fallback`: antigravity -> codex -> xai -> tavily; `default_provider` bypasses orchestration.
- Retryable host statuses are exactly 429, 503, 502 (`isRetryableHTTPStatus`); only those advance to the next backend and bump penalties.
- Penalties are process-local and unconfigured; tests reset them with `resetBackendPenaltiesForTest`.
- Config key and declared plugin ID are `claude-web-search-router` (`pluginIdentifier`), independent of the Makefile's language suffix.
- Tests are flat `*_test.go` beside each concern; run `go test ./...` from `go/` (nested module, root tests skip it).
- Linux build from repository root: `make -C examples/plugin BIN_DIR=bin bin/claude-web-search-router-go.so`; no c/rust peers exist.
- `go/model_resolve.go` is authoritative for defaults; the Codex config-field description in `go/main.go` still names the older model.
- `go/model_resolve.go` imports root `internal/registry` through the module `replace`; registry changes can break this example.

## ANTI-PATTERNS
- Do not forward the client Claude model name to Codex/xAI; empty config falls back to `gpt-5.4-mini` / `grok-4.3`.
- Do not use the Claude model as `antigravity_model`; resolve a `SupportsWebSearch` antigravity model instead.
- Do not widen fallback triggers beyond `isRetryableHTTPStatus` without updating `hostHTTPStatusFromError` and penalty tests.
- Do not make penalties persistent or configurable without updating the README, which documents them as in-memory with no extra config.
- Do not rely on the captured-request fixture cited by `README.md`; it is absent from the repository.
