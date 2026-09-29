# PROJECT KNOWLEDGE BASE

**Generated:** 2026-09-29
**Commit:** 89de4153
**Branch:** master

## OVERVIEW
Go 1.26+ proxy providing OpenAI/Gemini/Claude/Codex compatible APIs, OAuth, round-robin load balancing, and native C ABI plugins; Gin/Logrus core, embeddable v7 SDK.
- Repository: https://github.com/router-for-me/CLIProxyAPI

## STRUCTURE
```text
cmd/AGENTS.md             # Executables, startup modes, catalog utilities
internal/AGENTS.md        # Internal package map and smaller domains
sdk/AGENTS.md             # Public facades and integration contracts
sdk/cliproxy/AGENTS.md    # Embedded service and runtime conductor
deploy/AGENTS.md          # Active deployment authority and retired packages
examples/plugin/AGENTS.md # Polyglot native ABI examples; nested modules
test/                     # Cross-package protocol/thinking tests
docs/                     # SDK guides and steering
```

## WHERE TO LOOK
Paths below lead to domain guidance; follow their child guides rather than duplicating local rules here.
| Task | Guidance | Notes |
|------|----------|-------|
| CLI/startup | `cmd/AGENTS.md`, `internal/cmd/AGENTS.md` | Server, login, discovery dispatch |
| HTTP/RESP and management | `internal/api/AGENTS.md`, `internal/api/handlers/AGENTS.md`, `internal/api/middleware/AGENTS.md` | Listener, routes, management, capture |
| Public protocol handlers | `sdk/api/handlers/AGENTS.md` | HTTP/SSE/WebSocket execution |
| Provider runtime | `internal/runtime/executor/AGENTS.md` | Implementations, not SDK contracts |
| Translation | `internal/translator/AGENTS.md`, `sdk/translator/AGENTS.md` | Provider child guides; default registry |
| Thinking/reasoning | `internal/thinking/AGENTS.md` | Canonical pipeline and appliers |
| Authentication | `internal/auth/AGENTS.md`, `sdk/auth/AGENTS.md`, `sdk/access/AGENTS.md` | Provider implementations, login/persistence, inbound callers |
| Embedded service | `sdk/cliproxy/AGENTS.md`, `sdk/cliproxy/auth/AGENTS.md` | Lifecycle and runtime credential conductor |
| Execution/session accounting | `sdk/cliproxy/executor/AGENTS.md`, `sdk/cliproxy/executionregistry/AGENTS.md`, `sdk/cliproxy/session/AGENTS.md`, `sdk/cliproxy/usage/AGENTS.md` | Contracts, dispatch ownership, identity, usage |
| Native plugins | `internal/pluginhost/AGENTS.md`, `internal/pluginstore/AGENTS.md`, `sdk/pluginabi/AGENTS.md`, `sdk/pluginapi/AGENTS.md` | Host/install implementation and public contracts |
| Home integration | `internal/home/AGENTS.md`, `internal/homeplugins/AGENTS.md` | RESP/JSON control plane and artifact reconciliation |
| Configuration and reload | `internal/config/AGENTS.md`, `internal/watcher/AGENTS.md`, `internal/store/AGENTS.md` | YAML preservation, synthesis, remote mirrors |
| Model catalogs | `internal/registry/AGENTS.md`, `internal/client/codex/AGENTS.md` | Registry/updater and Codex adapters; `--local-model` disables remote updates |
| Replay and payloads | `internal/cache/AGENTS.md`, `internal/signature/AGENTS.md`, `internal/util/AGENTS.md` | Consistency, provenance, schema/JSON helpers |
| Logs and delivery | `internal/logging/AGENTS.md`, `internal/redisqueue/AGENTS.md`, `internal/wsrelay/AGENTS.md` | Capture, in-memory queues, relay sessions |
| Discovery and terminal UI | `internal/discovery/AGENTS.md`, `internal/tui/AGENTS.md` | LAN discovery; management REST client |
| Deployment/native web plugins | `deploy/AGENTS.md`, `deploy/chatgpt-web-plugin/AGENTS.md`, `deploy/gemini-web-plugin/AGENTS.md` | Supported topology; Gemini guide links browser/UI children |
| Plugin examples | `examples/plugin/AGENTS.md` | Module-local builds and ABI smoke checks |

## CODE MAP
Reference methods/scopes are retained from writer evidence; rg counts are textual, not semantic caller totals.
| Symbol | Type | Location | Refs | Role |
|--------|------|----------|------|------|
| Config | struct | `internal/config/config.go` | 2217 rg (scanner) | Cross-system configuration |
| GetGlobalRegistry | func | `internal/registry/model_registry.go` | 677 rg (scanner) | Registry singleton |
| ModelInfo | struct | `internal/registry/model_registry.go` | 750 rg (scanner) | Capability metadata |
| Handler | struct | `internal/api/handlers/management/handler.go` | 364 LSP | Management state |
| Host | struct | `internal/pluginhost/host.go` | 301 LSP | Native plugin coordination |
| NewBaseAPIHandlers | function | `sdk/api/handlers/handlers.go` | >20 rg scanner; fresh lexical count 118, tests included | Shared protocol execution |
| Capabilities | struct | `sdk/pluginapi/types.go` | >20 rg scanner; fresh lexical count 446, tests included | Plugin feature advertisement |
| FromString | function | `sdk/translator/format.go` | 320 rg scanner, fresh lexical corroboration, tests included | Protocol identity |
| Register | function | `internal/translator/translator/translator.go` | 30 LSP, declaration excluded | Pair registration |
| NewExecutorUsageReporter | function | `internal/runtime/executor/helps/usage_helpers.go` | 55 cross-package rg, scanner c26 | Attempt usage/timing |
| NewCodexAutoExecutor | function | `internal/runtime/executor/codex_websockets_executor.go` | 10 LSP, declaration excluded, tests included | Registered HTTP/WebSocket facade |
| NewBuilder | function | `sdk/cliproxy/builder.go` | 13 LSP, declaration excluded, tests included | Embedding entry |

## CONVENTIONS
- Keep changes small and simple (KISS).
- Comments in English only; translate existing non-English comments when editing their code. Do not add non-English comments.
- Keep the existing language of user-visible strings in each file/area. New Markdown docs use English unless explicitly language-specific (e.g. `README_CN.md`).
- Follow `gofmt`; keep imports goimports-style; wrap errors with useful context.
- Shadowed variables use method suffixes: `errStart := server.Start()`.
- Wrap defer errors: `defer func() { if err := f.Close(); err != nil { log.Errorf(...) } }()`.
- Use logrus structured logging without leaking secrets/tokens.
- `internal/runtime/executor/` contains executors and their unit tests only; helper/supporting files belong under `internal/runtime/executor/helps/`.
- When modifying CLIProxyAPIHome-related features, check whether the CLIProxyAPIHome repository needs corresponding updates.

## ANTI-PATTERNS (THIS PROJECT)
- No standalone changes to `internal/translator/` as a rule; modify it only with broader changes elsewhere. For translator-only tasks, first run `gh repo view --json viewerPermission -q .viewerPermission`: `WRITE`, `MAINTAIN`, or `ADMIN` permits proceeding; otherwise file a GitHub issue with the goal, rationale, and intended implementation code, then stop further work.
- No `log.Fatal`/`log.Fatalf` (process termination); return errors and log via logrus instead.
- No panics in HTTP handlers; use logged errors and meaningful HTTP status codes.
- Timeouts are allowed only during credential acquisition; once upstream is connected, do not set timeouts for subsequent network behavior. Preserve all four intentional exceptions: Codex websocket liveness deadlines in `internal/runtime/executor/codex_websockets_executor.go`; session deadlines in `internal/wsrelay/session.go`; management APICall timeout in `internal/api/handlers/management/api_tools.go`; utility timeouts in `cmd/fetch_antigravity_models`.
- No wall-clock `time.Sleep` in TTL, expiration, ordering, or cache-eviction unit tests: timer granularity and CI jitter make them unreliable. Use `nowFunc` / mock clocks, explicit timestamps, or deterministic synchronization.
- Do not break the thinking pipeline's canonical representation -> per-provider translation architecture.
- Do not promote legacy sidecars/overlays or historical Compose defaults into supported CPA deployment guidance.

## UNIQUE STYLES
- Thinking: `internal/thinking/apply.go` `ApplyThinking()` parses suffixes (`suffix.go`; suffix overrides body), normalizes to canonical `ThinkingConfig` (`types.go`), normalizes/validates centrally (`validate.go`/`convert.go`), then emits provider-specific output through `ProviderApplier`.
- Native plugins use C ABI loaders, not Go's standard plugin package; ABI versioning is separate from RPC JSON schema versioning.
- Translators are mostly destination/source with blank-import registration; bidirectional Interactions bridges make filenames insufficient to determine routes.
- Home owns distributed cooldown/refresh and subscriber state; local auth must not independently mutate Home-owned credentials or schedule their cooldowns.

## COMMANDS
```bash
gofmt -w . # Format (required after Go changes)
go build -o cli-proxy-api ./cmd/server # Build
go run ./cmd/server # Run dev server
go test ./... # Run all tests
go test -v -run TestName ./path/to/pkg # Run single test
go build -o test-output ./cmd/server && rm test-output # Verify compile (REQUIRED after changes)
```
- Common flags: `--config <path>`, `--tui`, `--standalone`, `--local-model`, `--no-browser`, `--oauth-callback-port <port>`.
- Root tests do not traverse nested Go modules: active deployment plugins, plugin Go examples, and the realtime example require module-local commands; see their guides.

## NOTES
- Config: default `config.yaml`; full kebab-case reference `config.example.yaml`. `.env` auto-loads from the working directory; auth material defaults under `auths/`.
- Storage: file-based default; optional Postgres/git/object store use `PGSTORE_*`, `GITSTORE_*`, `OBJECTSTORE_*`. Home-synthesized config is authoritative for Home concurrency.
- `internal/managementasset/` owns management HTML, not config snapshots; usage/token accounting is in `sdk/cliproxy/usage/` (there is no `internal/usage/`).
- Supported CPA topology is core, PostgreSQL, and native plugins. Core convergence uses checksum-verified artifacts and direct Docker stop/start, not Compose or automatic rollback.
- CI closes PRs changing root or nested AGENTS.md and rejects translator changes; repository permission does not bypass these independent guards.
