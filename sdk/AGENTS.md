# SDK PACKAGE MAP

## OVERVIEW
Public v8 embedding and extension surfaces; score 14 across the subtree, with 13 immediate package directories. Paths below are relative to `sdk/`.

Parent policy: [repository guide](../AGENTS.md).

## STRUCTURE
| Package | Role |
|---------|------|
| `access/` | Incoming request credential validation. |
| `api/` | Public server options and management/OAuth-session facade; protocol implementation in `api/handlers/`. |
| `auth/` | Interactive provider login and token persistence. |
| `cliproxy/` | Embeddable proxy service; follow `cliproxy/AGENTS.md`. |
| `config/` | Aliases and forwarding functions for `internal/config`. |
| `httptransport/` | Context-carried decorators around selected transports; observation and suppression seam. |
| `logging/` | Public request logger aliases and file-logger constructors. |
| `pluginabi/` | Native plugin RPC envelope, method identifiers, compatibility versions. |
| `pluginapi/` | Plugin capability interfaces and request/response contracts. |
| `pluginhost/` | Public host configuration/model conversion; follow `pluginhost/AGENTS.md`. |
| `pluginstore/` | Artifact clients and install facade; follow `pluginstore/AGENTS.md`. |
| `proxyutil/` | Shared proxy-setting parser, HTTP transports, and connection dialers. |
| `translator/` | Translation registry and middleware; `translator/builtin/` enables built-in registrations. |

## CHILD GUIDES
| Guide | Role |
|-------|------|
| `access/AGENTS.md` | Ordered inbound credential providers and error precedence. |
| `api/handlers/AGENTS.md` | Shared execution and protocol HTTP/SSE/WebSocket contracts. |
| `auth/AGENTS.md` | Login persistence and credential-aware Codex refresh writes. |
| `cliproxy/AGENTS.md` | Embedded lifecycle, config commit/apply, credential sync and model registration. |
| `cliproxy/auth/AGENTS.md` | Credential execution, scheduling, cooldown, refresh and Home selection. |
| `cliproxy/executionregistry/AGENTS.md` | Subscriber-lifetime dispatch reservations, scopes, release tickets and barriers. |
| `cliproxy/executor/AGENTS.md` | Request/options/stream contracts, transport context and lifecycle. |
| `cliproxy/session/AGENTS.md` | Session identity extraction, derivation and Merkle prefix affinity. |
| `cliproxy/usage/AGENTS.md` | Usage-plugin delivery queue and provider-aware token accounting. |
| `pluginabi/AGENTS.md` | Native RPC envelope and compatibility gates. |
| `pluginapi/AGENTS.md` | Optional capabilities and buffered frontend route contracts. |
| `pluginhost/AGENTS.md` | Public configuration/model/quota conversions. |
| `pluginstore/AGENTS.md` | Artifact client and shared cooldown identity. |
| `translator/AGENTS.md` | Format dispatch, middleware and retained translation failures. |

## WHERE TO LOOK
| Task | Location | Detail |
|------|----------|--------|
| Configure embedded routes | `api/options.go` | Engine hook runs before routes; router hook after routes. |
| Embed OAuth management | `api/management.go` | Handler constructors and session/callback forwarders. |
| Preserve configuration comments | `config/config.go` | Public preserve-comments save functions. |
| Observe selected HTTP transports | `httptransport/decorator.go` | Decorate without replacing transport selection; WithoutDecorator suppresses wrapping. |
| Embed plugin lifecycle and quota | `pluginhost/AGENTS.md` | Public host contracts and configuration ownership. |
| Fetch/install plugin artifacts | `pluginstore/AGENTS.md` | Client auth, egress identity, manifest/release entry points. |
| Interpret proxy overrides | `proxyutil/proxy.go` | Empty inherits; direct/none bypass; concrete proxy URLs select a transport. |

## CONVENTIONS
- Thin public facades use aliases and forwarding calls; implementation lives in the corresponding internal package.
- Current imports use `github.com/router-for-me/CLIProxyAPI/v8`; the `docs/sdk-*.md` guides use that path and only public `sdk/...` packages.
- Package-local checks: `go test ./sdk/<package>`; handler descendants require `go test ./sdk/api/handlers/...`.
- `proxyutil.BuildHTTPTransport` may return nil transport with ModeInherit and no error; callers retain inherited behavior.
- `httptransport` decorators delegate without retaining secrets or consuming bodies; they do not observe WebSocket traffic after upgrade.
- `cliproxy/pipeline/` has no in-repo importers; its context helper is not an installed middleware chain.

## ANTI-PATTERNS
- Do not duplicate internal config, logging, or marketplace implementations inside their public facades.
- Do not conflate inbound API-key access (`access`) with provider login credentials (`auth`).
- Do not treat `direct`/`none` proxy settings as empty inheritance or use Parse alone to validate execution overrides; use ValidRequestProxy.
