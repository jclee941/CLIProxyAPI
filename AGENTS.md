# PROJECT KNOWLEDGE BASE

**Generated:** 2026-10-15
**Commit:** e0b94fa7
**Branch:** master

## OVERVIEW
Go 1.26+ proxy providing OpenAI/Gemini/Claude/Codex compatible APIs, OAuth, round-robin load balancing, and native C ABI plugins; Gin/Logrus core, embeddable v8 SDK.
- Repository: https://github.com/router-for-me/CLIProxyAPI

## STRUCTURE
```text
cmd/AGENTS.md             # Executable dispatch, catalog tools, retained Telegram commands
internal/AGENTS.md        # Internal package map and smaller domains
sdk/AGENTS.md             # Public facades, transport observation, extension contracts
sdk/cliproxy/AGENTS.md     # Embedded lifecycle, config commit/apply, credential sync
deploy/AGENTS.md          # Active deployment authority and ancillary packages
examples/plugin/AGENTS.md # Polyglot native ABI examples; nested modules
test/                     # Cross-package protocol/thinking tests
docs/                     # SDK guides and steering
```

## WHERE TO LOOK
Paths below lead to domain guidance; follow their child guides rather than duplicating local rules here.
| Task | Guidance | Notes |
|------|----------|-------|
| Repository/domain indexes | `AGENTS.md`, `internal/AGENTS.md`, `sdk/AGENTS.md` | Root policy; internal packages; public embedding facades |
| CLI/startup | `cmd/AGENTS.md`, `internal/cmd/AGENTS.md` | Server, login, SIWC bind, discovery dispatch |
| HTTP/RESP and management | `internal/api/AGENTS.md`, `internal/api/handlers/AGENTS.md`, `internal/api/middleware/AGENTS.md` | Listener, routes, management, capture |
| Public protocol handlers | `sdk/api/handlers/AGENTS.md` | HTTP/SSE/WebSocket execution |
| Provider runtime | `internal/runtime/executor/AGENTS.md` | Implementations, not SDK contracts |
| Translation | `internal/translator/AGENTS.md`, `sdk/translator/AGENTS.md` | Provider child guides; default registry |
| Translation primitives/providers | `internal/translator/common/AGENTS.md`, `internal/translator/antigravity/AGENTS.md`, `internal/translator/claude/AGENTS.md`, `internal/translator/codex/AGENTS.md`, `internal/translator/gemini/AGENTS.md`, `internal/translator/openai/AGENTS.md` | Shared refusal/apply_patch policy; inspect registrations for direction |
| Thinking/reasoning | `internal/thinking/AGENTS.md` | Canonical pipeline and appliers |
| Authentication | `internal/auth/AGENTS.md`, `sdk/auth/AGENTS.md`, `sdk/access/AGENTS.md` | Provider implementations, login/persistence, inbound callers |
| Provider authentication | `internal/auth/claude/AGENTS.md`, `internal/auth/codex/AGENTS.md`, `internal/auth/devin/AGENTS.md`, `internal/auth/kimi/AGENTS.md` | Device identity, Codex PKCE/SIWC and file locking, session records, domain-aware refresh |
| Embedded service | `sdk/cliproxy/AGENTS.md`, `sdk/cliproxy/auth/AGENTS.md` | Lifecycle and runtime credential conductor |
| Execution/session accounting | `sdk/cliproxy/executor/AGENTS.md`, `sdk/cliproxy/executionregistry/AGENTS.md`, `sdk/cliproxy/session/AGENTS.md`, `sdk/cliproxy/usage/AGENTS.md` | Contracts, dispatch ownership, identity, usage |
| Native plugins | `internal/pluginhost/AGENTS.md`, `internal/pluginstore/AGENTS.md`, `sdk/pluginabi/AGENTS.md`, `sdk/pluginapi/AGENTS.md` | Host/install implementation and public contracts |
| Plugin embedding facades | `sdk/pluginhost/AGENTS.md`, `sdk/pluginstore/AGENTS.md` | Model/quota conversion; install client and network-scope cooldown identity |
| Transport observation | `sdk/AGENTS.md`, `sdk/httptransport/decorator.go` | Decorate selected transports; preserve proxy, fingerprint, pool and protocol behavior |
| Home integration | `internal/home/AGENTS.md`, `internal/homeplugins/AGENTS.md` | RESP/JSON control plane and artifact reconciliation |
| Configuration and reload | `internal/config/AGENTS.md`, `internal/watcher/AGENTS.md`, `internal/store/AGENTS.md` | YAML preservation, synthesis, remote mirrors |
| Reload details | `internal/watcher/diff/AGENTS.md`, `internal/watcher/synthesizer/AGENTS.md` | Deterministic summaries; config/file credentials to runtime records |
| Model catalogs | `internal/registry/AGENTS.md`, `internal/client/codex/AGENTS.md` | Registry/updater and Codex adapters; `--local-model` disables remote updates |
| Replay and payloads | `internal/cache/AGENTS.md`, `internal/signature/AGENTS.md`, `internal/util/AGENTS.md` | Consistency, provenance, schema/JSON helpers |
| Shared miscellaneous helpers | `internal/misc/AGENTS.md` | Retained existing guide; conservative score below creation threshold |
| Logs and delivery | `internal/logging/AGENTS.md`, `internal/redisqueue/AGENTS.md`, `internal/wsrelay/AGENTS.md` | Capture, in-memory queues, relay sessions |
| Discovery and terminal UI | `internal/discovery/AGENTS.md`, `internal/tui/AGENTS.md` | LAN discovery; management REST client |
| Deployment/native web plugins | `deploy/AGENTS.md`, `deploy/chatgpt-web-plugin/AGENTS.md`, `deploy/gemini-web-plugin/AGENTS.md`, `deploy/flow2api-plugin/AGENTS.md` | NAS convergence; ChatGPT, Gemini and Flow native artifacts |
| Shared portal/browser companion | `deploy/gemini-web-plugin/web/AGENTS.md`, `deploy/gemini-web-plugin/browser-extension/AGENTS.md` | Shared Gemini/Flow frontend; production versus QA packaging |
| Plugin examples | `examples/plugin/AGENTS.md`, `examples/plugin/claude-web-search-router/AGENTS.md` | Module-local builds, ABI smoke checks, Go-only search router |

## CODE MAP
Digest evidence retains measurement method/scope: rg counts are textual/import counts; LSP excludes declarations but includes tests and receiver references, not unique callers.
| Symbol | Type | Location | Refs | Role |
|--------|------|----------|------|------|
| Config | struct | `internal/config/config.go` | Package imported by 490 files (rg) | Cross-system configuration |
| GetGlobalRegistry | func | `internal/registry/model_registry.go` | Package imported by 227 files (rg) | Registry singleton |
| ApplyThinking | func | `internal/thinking/apply.go` | Package imported by 80 files (rg) | Canonical thinking entry |
| GetGJSONBytesNoCopy | func | `internal/util/gjson.go` | Package imported by 133 files (rg) | Caller-owned JSON bytes |
| Handler | struct | `internal/api/handlers/management/handler.go` | 413 LSP refs | Management state |
| Host | struct | `internal/pluginhost/host.go` | 311 LSP refs | Native plugin coordination |
| Auth | struct | `sdk/cliproxy/auth/types.go` | Package imported by 432 files (rg; excludes own package) | Runtime credential identity/state |
| Service | struct | `sdk/cliproxy/service.go` | 323 rg -w matches in sdk/cliproxy/*.go, internal/cmd and examples | Embedded runtime conductor |
| Capabilities | struct | `sdk/pluginapi/types.go` | 258 LSP refs | Optional plugin features |
| FromString | function | `sdk/translator/format.go` | 380 LSP refs | Protocol identity |
| Register | function | `internal/translator/translator/translator.go` | 30 call sites in internal/translator (rg) | Pair registration |
| NewExecutorUsageReporter | function | `internal/runtime/executor/helps/usage_helpers.go` | 61 LSP refs in 29 files | Per-attempt usage/TTFT |
| NewCodexWebsocketsExecutor | function | `internal/runtime/executor/codex_websockets_executor.go` | 98 LSP refs in 26 files | WebSocket transport behind auto executor |
| failure | function | `deploy/gemini-web-plugin/wire.go` | 577 LSP refs | Native session/media typed errors |
| service | struct | `deploy/flow2api-plugin/plugin.go` | 127 LSP refs | Flow generation/resource dispatcher |

## CONVENTIONS
- Keep changes small and simple (KISS).
- Comments in English only; translate existing non-English comments when editing their code. Do not add non-English comments.
- Keep the existing language of user-visible strings in each file/area. New Markdown docs use English unless explicitly language-specific (e.g. `README_CN.md`).
- Follow `gofmt`; keep imports goimports-style; wrap errors with useful context.
- Shadowed variables use method suffixes: `errStart := server.Start()`.
- Wrap defer errors: `defer func() { if err := f.Close(); err != nil { log.Errorf(...) } }()`.
- Use logrus structured logging without leaking secrets/tokens.
- `internal/runtime/executor/` is for executors and their unit tests; new helper/supporting files belong under `internal/runtime/executor/helps/` (legacy flat support files remain).
- Payload configuration is the final semantic barrier before every executor send, including streaming, WebSocket, continuation, retry/fallback, image, and token-count paths. Finish built-in translation, normalization, injection, and cleanup first, then apply user payload rules exactly once; afterwards only transport framing, serialization, signature calculation, and read-only validation may run. Add regression tests when introducing or changing request-building paths.
- Upstream treats endpoints under /v0/management as deprecated: do not change them for features unless a compile fix requires it. They remain supported; the independent v8 contract is in `internal/api/server_management_v8.go` and `docs/management-api-v8.md`.
- When modifying CLIProxyAPIHome-related features, check whether the CLIProxyAPIHome repository needs corresponding updates.

## ANTI-PATTERNS (THIS PROJECT)
- No standalone changes to `internal/translator/` as a rule; modify it only with broader changes elsewhere. For translator-only tasks, first run `gh repo view --json viewerPermission -q .viewerPermission`: `WRITE`, `MAINTAIN`, or `ADMIN` permits proceeding; otherwise file a GitHub issue with the goal, rationale, and intended implementation code, then stop further work.
- No `log.Fatal`/`log.Fatalf` (process termination); return errors and log via logrus instead.
- No panics in HTTP handlers; use logged errors and meaningful HTTP status codes.
- Timeouts are allowed only during credential acquisition; once upstream is connected, do not set timeouts for subsequent network behavior. Preserve intentional exceptions: Codex websocket liveness deadlines in `internal/runtime/executor/codex_websockets_connection.go`; session deadlines in `internal/wsrelay/session.go`; management APICall timeout in `internal/api/handlers/management/api_tools.go`; utility timeouts in `cmd/fetch_antigravity_models`; the 10s npm version fetch in `internal/runtime/executor/helps/xai_version.go` (not an upstream model call).
- No wall-clock `time.Sleep` in TTL, expiration, ordering, or cache-eviction unit tests: timer granularity and CI jitter make them unreliable. Use `nowFunc` / mock clocks, explicit timestamps, or deterministic synchronization.
- Do not break the thinking pipeline's canonical representation -> per-provider translation architecture.
- Do not promote legacy sidecars/overlays into supported CPA deployment guidance.
- Home caches are exclusive: never fall back to local cache on a Home miss. No-copy JSON aliases caller bytes; do not mutate them in place.
- Never send a request envelope with `RequestEnvelope.Err`; byte-only translation APIs do not expose that error. Request/credential-scoped error markers control retry, rotation and penalties.

## UNIQUE STYLES
- Thinking: `internal/thinking/apply.go` `ApplyThinking()` parses suffixes (`suffix.go`; suffix overrides body), normalizes to canonical `ThinkingConfig` (`types.go`), normalizes/validates centrally (`validate.go`, `convert.go`), then emits provider-specific output through `ProviderApplier`.
- Native plugins use C ABI loaders, not Go's standard plugin package; ABI 1 and RPC JSON schema 6 are separately versioned. Schema-6 credential-owned stop rules use request-scoped errors.
- Translators are mostly destination/source with blank-import registration; bidirectional Interactions bridges make filenames insufficient to determine routes.
- Home owns distributed cooldown/refresh and subscriber state; local auth must not independently mutate Home-owned credentials or schedule their cooldowns.
- Blank imports activate translator pairs in `internal/translator/init.go` and thinking appliers in `internal/runtime/executor/helps/thinking_providers.go`; new providers need registration and activation.
- `internal/translator/common/` shares unsupported-part/user-turn refusal policy and the Responses apply_patch bridge with executors; request-envelope converters carry pinned tool-search context.
- Payload finalizers in `internal/runtime/executor/helps/payload_finalizer.go` restart each rebuilt attempt from the unconfigured body; barrier/review tests guard exactly-once rules.
- Config v8 aliases translate only at YAML boundaries: canonical paths win by presence, equal-endpoint legacy entries never merge. Management GET normalizes views without migration; successful mutations persist v8 layout; JSON strips ICE secrets and adds auth indexes, raw YAML stays separate.
- Credential lock order: reload barrier -> credential gate -> manager mutex; Store callbacks may read, never mutate the manager. Coalesced structural notifications require current-state reads and unsubscribe.
- Codex SIWC credentials share the outer credential file; partition-scoped refresh patches merge under `WithFileLock` without replacing independent rotations or disk configuration.
- Tracked HTTP delivery acknowledgement is independent of cancellation: resolve every exit; producers close output before waiting. Home distinguishes ambiguous dispatch from unaccepted work; release tickets require acknowledged cumulative sequences.

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
- Active native plugin modules use `make check` (race/shuffle, vet, C-shared build, loader smoke); convergence checks run `python3 -m unittest cpa_converge_test` from `deploy/`. Concurrency changes in cache, Claude auth, watcher and wsrelay require race checks.

## NOTES
- Config: default `config.yaml`; full kebab-case reference `config.example.yaml`. `.env` auto-loads from the working directory; auth material defaults under `auths/`.
- Storage: file-based default; remote selection is `PGSTORE_DSN` > `OBJECTSTORE_ENDPOINT` > `GITSTORE_GIT_URL` (families `PGSTORE_*`, `OBJECTSTORE_*`, `GITSTORE_*`); only PostgresStore supplies cooldown state. Home-synthesized config is authoritative for Home concurrency.
- `internal/managementasset/` owns management HTML, not config snapshots; usage/token accounting is in `sdk/cliproxy/usage/` (no internal usage package).
- Supported CPA topology is core, PostgreSQL, and native plugins. Core convergence uses checksum-verified artifacts and direct Docker stop/start, not Compose or automatic rollback.
- CI closes PRs changing root or nested AGENTS.md and rejects translator changes; repository permission does not bypass these independent guards.
- Current deployment authority: `deploy/README.md`, NAS 192.168.50.215 at /volume1/docker/cliproxy; explicit convergence as jclee with `/usr/local/bin` on PATH (script defaults are the NAS paths and `deploy/cpa-plugins.json`; it never runs Compose). `deploy/pull-deploy.sh` uses a dedicated deploy clone, rejects tracked modifications and prevents overlap with non-blocking flock.
- `deploy/cpa-plugins.json` selects gemini-web (restart), chatgpt-web and flow2api (hot), dashboards and companion ZIP. Core uses checksum-verified cpa-core releases; hot-load proof is a versioned host log line; restart health checks loaded plugins, ready accounts and `X-CPA-COMMIT`.
- Gemini's web build emits both Gemini and Flow dashboards; ChatGPT's dashboard is hand-authored. QA companion archives are not production login packages. Stop captured account profiles while CPA owns rotating cookies; keep the signed-out Flow captcha browser running.
- Model catalogs have embedded fallbacks, per-catalog `CatalogSources` overrides and a 3h refresh interval; `--local-model` disables remote updates. Release automation refreshes/validates catalogs via `.github/scripts/refresh-model-catalogs.sh`; server build metadata uses `main.Version`, `main.Commit`, `main.BuildDate`.
- GitHub token precedence: configured value, environment, eligible Git-store fallback; `internal/githubauth/token.go` releases tokens only to HTTPS api.github.com. Plugin request-kind/URL auth precedence remains separate.
- Error-only request capture: known bodies up to 1 MiB eager, deferred up to 32 MiB; full logging clears the deferred cap, native handlers retain read bounds. TUI supports remote HTTP(S) origins but still uses v0 management.
- Video creator auth/model bindings default to 3h, overridable with positive `VideoResultAuthCacheTTL`. SDK plugin-store `WithNetworkScope` labels shared cooldown identity, not outbound transport.
- CI core-build runs root tests for relevant master changes, ignoring Markdown; PRs build the server; main-target PRs retarget to dev. Root README translations are parallel; SDK guides and nested example modules use the v8 module path (example modules stay under `.../v8/examples/` so they may import root `internal/` packages).
