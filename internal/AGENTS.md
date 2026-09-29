# INTERNAL PACKAGE MAP

## OVERVIEW
Implementation package index; score 14 from owned-subtree size, package fan-out, symbol density, and centrality.

## STRUCTURE
One row per immediate package area; specialized guidance lives below these paths.

| Package | Responsibility |
|---------|----------------|
| `access/` | Reconcile inbound access providers; `config_access/` supplies static API-key authentication |
| `api/` | HTTP server boundary; see its own guidance |
| `auth/` | Provider login, refresh, and token-file formats |
| `browser/` | Cross-platform OAuth browser launching |
| `buildinfo/` | Version, commit, and build date supplied by server startup |
| `cache/` | Signature and provider-specific reasoning replay state |
| `client/` | Client dialect adapters; Claude model cloaking, Codex integration, Grok keepalives |
| `clienterror/` | HTTP error classification and credential-rotation eligibility |
| `cmd/` | CLI command orchestration behind the executable |
| `config/` | Configuration schema, loading, normalization, and preserving YAML writes |
| `constant/` | Shared provider and protocol identifiers |
| `credentialweight/` | Numeric credential-weight parsing and bounds |
| `discovery/` | LAN gateway DNS-SD advertisement and discovery |
| `home/` | Control-plane integration; see its own guidance |
| `homeplugins/` | Home plugin integration; see its own guidance |
| `htmlsanitize/` | Browser-facing string and JSON sanitization |
| `httpfetch/` | Size-bounded HTTP downloads |
| `httpwire/` | Ordered HTTP/1.1 request headers at the connection layer |
| `interfaces/` | Shared handler, message, error, and translation contracts |
| `logging/` | Application diagnostics, request transcripts, and forwarding |
| `managementasset/` | Management HTML download and cache updater |
| `misc/` | OAuth callbacks, metadata merge, MIME table, and client-version helpers |
| `modelconfig/` | Shared model metadata normalization and model-list hashes |
| `pluginhost/` | Plugin runtime; see its own guidance |
| `pluginstore/` | Plugin storage; see its own guidance |
| `redisqueue/` | In-memory usage/error event queues, despite the name |
| `registry/` | Dynamic model availability and embedded catalog snapshots |
| `runtime/` | Upstream execution; follow executor guidance below this path |
| `safemode/` | Block active proxy use of shipped example API keys |
| `signature/` | Reasoning signature inspection, compatibility, and sanitization |
| `store/` | Git, object-store, and PostgreSQL credential mirrors |
| `thinking/` | Reasoning effort, budgets, summary intent, and provider appliers |
| `translator/` | Protocol conversion; see its own guidance |
| `tui/` | Terminal management client |
| `util/` | Shared schema, tool identity, header, and JSON utilities |
| `watcher/` | Config/auth reload, credential synthesis, and update revisions |
| `wsrelay/` | HTTP-over-WebSocket provider sessions |

## WHERE TO LOOK
| Change | Starting point |
|--------|----------------|
| CLI mode or login dispatch | `cmd/`, then executable wiring in `../cmd/server/` |
| Credential source becomes runtime auth | `watcher/`, then `../sdk/cliproxy/` |
| Model listing disagrees with execution | `registry/`, `modelconfig/`, then relevant client adapter |
| Signature replay rejection | `signature/` and `cache/`; these are separate concerns |
| Small shared wire contract | `interfaces/`, `httpwire/`, or `clienterror/`, not a new provider implementation |

## ANTI-PATTERNS
- Do not treat `clienterror.IsRequestFault` as a generic 4xx check: 402/429 and model-capability failures can require credential rotation.
- Do not retry ambiguous partial HTTP headers in `httpwire`; the connection wrapper reports a connection error deliberately.
- Do not infer that `managementasset` owns configuration snapshots; configuration belongs to `config/`.
- Do not invent missing legacy areas such as `api/modules/amp` or `usage/`; use the package map above.
