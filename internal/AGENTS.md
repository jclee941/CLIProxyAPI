# INTERNAL PACKAGE MAP

## OVERVIEW
Index of implementation packages and every child guide; score 14 (38 package areas, high fan-out and centrality). Paths are relative to `internal/`.

## GUIDED AREAS
| Area | Role | Child guides |
|------|------|--------------|
| `api/` | HTTP/RESP listener, routes, reload, plugin fallback | `api/AGENTS.md`; `api/handlers/AGENTS.md` (management persistence, OAuth, v8 views; covers `api/handlers/management/`); `api/middleware/AGENTS.md` (deferred capture, streaming delivery) |
| `auth/` | Provider login/refresh; parent covers antigravity, meta, vertex, xai, empty | `auth/AGENTS.md`; `auth/claude/AGENTS.md` (OAuth, device identity, uTLS); `auth/codex/AGENTS.md` (PKCE, JWT introspection); `auth/devin/AGENTS.md` (session tokens, quota records); `auth/kimi/AGENTS.md` (domain-aware device flow) |
| `cache/` | Signature/reasoning replay, Home KV vs local, Interactions sessions | `cache/AGENTS.md` |
| `client/` | Client dialect adapters; `client/claude/models/` and `client/grokbuild/` are unguided | `client/codex/AGENTS.md` (catalogs, realtime relay, apply-patch, tool-schema, multi-agent v2) |
| `cmd/` | Login wrappers, service lifecycle, discovery CLI, Vertex import | `cmd/AGENTS.md` |
| `config/` | Schema, v8 YAML aliases, comment-preserving saves, runtime clones | `config/AGENTS.md` |
| `discovery/` | DNS-SD identity, interface filtering, advertise/browse | `discovery/AGENTS.md` |
| `home/` | RESP control plane, dispatch fencing, KV, release acks | `home/AGENTS.md` |
| `homeplugins/` | Platform artifact reconciliation, install/load status | `homeplugins/AGENTS.md` |
| `logging/` | App logs, request transcripts, body spools, Home forwarding | `logging/AGENTS.md` |
| `misc/` | OAuth, metadata, MIME, client-version helpers (score 6, retained) | `misc/AGENTS.md` |
| `pluginhost/` | Native lifecycle, capability adapters, scoped RPC/frontend bridges | `pluginhost/AGENTS.md` |
| `pluginstore/` | Catalogs, authenticated downloads, cooldowns, validated installs | `pluginstore/AGENTS.md` |
| `redisqueue/` | In-memory usage/error queues, usage plugin serialization | `redisqueue/AGENTS.md` |
| `registry/` | Model registry, capability projections, embedded/remote catalogs (`registry/models/`) | `registry/AGENTS.md` |
| `runtime/` | Container only; provider executors plus `runtime/executor/helps/` | `runtime/executor/AGENTS.md` |
| `signature/` | Signature shape/provider compatibility, history sanitization; `signature/signaturetest/` fixtures | `signature/AGENTS.md` |
| `store/` | Git/object-store/PostgreSQL credential mirrors, cooldown store | `store/AGENTS.md` |
| `thinking/` | Canonical thinking pipeline; `thinking/provider/` self-registering appliers | `thinking/AGENTS.md` |
| `translator/` | Registry hub, blank-import activation, facade `translator/translator/` | `translator/AGENTS.md`; `translator/common/AGENTS.md` (shared primitives); domains `translator/antigravity/AGENTS.md`, `translator/claude/AGENTS.md`, `translator/codex/AGENTS.md`, `translator/gemini/AGENTS.md`, `translator/openai/AGENTS.md` |
| `tui/` | REST management UI, configurable origin, OAuth, log polling | `tui/AGENTS.md` |
| `util/` | Schema cleaners, tool identity maps, no-copy JSON, headers | `util/AGENTS.md` |
| `watcher/` | Config/auth reload, revisioned update dispatch | `watcher/AGENTS.md`; `watcher/diff/AGENTS.md` (change summaries, model hashes); `watcher/synthesizer/AGENTS.md` (config keys/auth files to runtime auths) |
| `wsrelay/` | HTTP-over-WebSocket provider sessions | `wsrelay/AGENTS.md` |

## UNGUIDED PACKAGES
| Package | Role |
|---------|------|
| `access/` | Inbound access provider reconciliation; `access/config_access/` static API keys |
| `clienterror/` | Status extraction, request-fault classification driving credential penalties |
| `constant/` | Provider/protocol name constants |
| `credentialweight/` | Credential weight parse/normalize with max bound |
| `httpwire/` | Ordered request headers, error-aware flushing through wrappers |
| `interfaces/` | Handler and Gemini content types, aliases to SDK translator funcs |
| `managementasset/` | Management HTML updater, static path selection, download cache |
| `modelconfig/` | Model info resolution, model-list hashes consumed by `watcher/diff/` |
| Small helpers | `browser/` OAuth URL launch; `buildinfo/` executable version metadata; `githubauth/` configured GitHub token; `htmlsanitize/` string/JSON escaping; `httpfetch/` status-checked, size-capped GET; `safemode/` shipped example-key warning |

## WHERE TO LOOK
| Change | Starting point |
|--------|----------------|
| CLI mode or login dispatch | `cmd/`, then `../cmd/server/` |
| Credential source becomes runtime auth | `watcher/synthesizer/`, then `../sdk/cliproxy/` |
| Model listing disagrees with execution | `registry/`, `modelconfig/`, then the client adapter |
| Signature replay rejection | `signature/` and `cache/`; separate concerns |
| New translator pair or thinking applier | blank imports in `translator/init.go` / `runtime/executor/helps/thinking_providers.go` |
| Small shared wire contract | `interfaces/`, `httpwire/`, or `clienterror/`, not a provider implementation |

## CONVENTIONS
- Guides stop at depth three: `runtime/executor/helps/`, `api/handlers/management/`, and `client/codex/` subpackages are covered by the nearest parent guide.
- Hyphenated `client/codex/` dirs use package names applypatch, multiagentv2, toolschema.
- `buildinfo/` receives executable metadata from `../cmd/server/main.go`, not direct package ldflags.
- Tiny packages stay in this map instead of earning guides; scores come from rg/LSP evidence.

## ANTI-PATTERNS
- Do not treat `clienterror.IsRequestFault` as a generic 4xx check: 402/429 and model-capability failures can require credential rotation.
- Do not retry ambiguous partial HTTP headers in `httpwire/`; the connection wrapper reports a connection error deliberately.
- Do not hand `githubauth/` tokens to non-HTTPS or non-api.github.com URLs; go through `TokenForURL`.
- Do not add a translator pair or thinking applier without its blank import; unimported packages are dead code.
