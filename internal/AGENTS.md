# INTERNAL PACKAGE MAP

## OVERVIEW
Implementation package index; score 14 from owned-subtree size, package fan-out, symbol density, and centrality.

## STRUCTURE
One row per immediate package area; specialized guidance lives below these paths.

| Package | Responsibility |
|---------|----------------|
| `access/` | Reconcile inbound access providers; `config_access/` supplies static API-key authentication |
| `api/` | HTTP server boundary; `api/AGENTS.md` |
| `auth/` | Provider login, refresh, and token-file formats |
| `browser/` | Cross-platform OAuth browser launching |
| `buildinfo/` | Version, commit, and build date supplied by server startup |
| `cache/` | Signature and reasoning replay; `cache/AGENTS.md` |
| `client/` | Client dialect adapters; Claude model cloaking, Codex integration, Grok keepalives |
| `clienterror/` | HTTP error classification and credential-rotation eligibility |
| `cmd/` | CLI orchestration; `cmd/AGENTS.md` |
| `config/` | Configuration schema, loading, normalization, and preserving YAML writes |
| `constant/` | Shared provider and protocol identifiers |
| `credentialweight/` | Numeric credential-weight parsing and bounds |
| `discovery/` | LAN DNS-SD; `discovery/AGENTS.md` |
| `home/` | Control-plane integration; `home/AGENTS.md` |
| `homeplugins/` | Home plugin integration; `homeplugins/AGENTS.md` |
| `htmlsanitize/` | Browser-facing string and JSON sanitization |
| `httpfetch/` | Size-bounded HTTP downloads |
| `httpwire/` | Ordered HTTP/1.1 request headers at the connection layer |
| `interfaces/` | Shared handler, message, error, and translation contracts |
| `logging/` | Diagnostics and request capture; `logging/AGENTS.md` |
| `managementasset/` | Management HTML download and cache updater |
| `misc/` | OAuth, metadata, MIME, and client-version helpers; `misc/AGENTS.md` |
| `modelconfig/` | Shared model metadata normalization and model-list hashes |
| `pluginhost/` | Plugin runtime; `pluginhost/AGENTS.md` |
| `pluginstore/` | Plugin storage; `pluginstore/AGENTS.md` |
| `redisqueue/` | In-memory event queues; `redisqueue/AGENTS.md` |
| `registry/` | Dynamic model availability and embedded catalog snapshots |
| `runtime/` | Upstream execution; `runtime/executor/AGENTS.md` |
| `safemode/` | Block active proxy use of shipped example API keys |
| `signature/` | Reasoning signature handling; `signature/AGENTS.md` |
| `store/` | Git, object-store, and PostgreSQL credential mirrors |
| `thinking/` | Reasoning configuration/appliers; `thinking/AGENTS.md` |
| `translator/` | Protocol conversion; `translator/AGENTS.md` |
| `tui/` | Terminal management client; `tui/AGENTS.md` |
| `util/` | Shared schema, tool identity, header, and JSON utilities |
| `watcher/` | Config/auth reload, credential synthesis, and update revisions |
| `wsrelay/` | HTTP-over-WebSocket sessions; `wsrelay/AGENTS.md` |

## WHERE TO LOOK
| Change | Starting point |
|--------|----------------|
| CLI mode or login dispatch | `cmd/`, then executable wiring in `../cmd/server/` |
| Credential source becomes runtime auth | `watcher/`, then `../sdk/cliproxy/` |
| Model listing disagrees with execution | `registry/`, `modelconfig/`, then relevant client adapter |
| Signature replay rejection | `signature/` and `cache/`; these are separate concerns |
| Small shared wire contract | `interfaces/`, `httpwire/`, or `clienterror/`, not a new provider implementation |

## CONVENTIONS
- Scores use source-tree size and LSP declarations; tiny packages stay in this map.
- `buildinfo/` receives executable metadata from `../cmd/server/main.go`, not direct package ldflags.

## ANTI-PATTERNS
- Do not treat `clienterror.IsRequestFault` as a generic 4xx check: 402/429 and model-capability failures can require credential rotation.
- Do not retry ambiguous partial HTTP headers in `httpwire`; the connection wrapper reports a connection error deliberately.
- Do not infer that `managementasset` owns configuration snapshots; configuration belongs to `config/`.
- Do not invent missing legacy areas such as `api/modules/amp` or `usage/`; use the package map above.
