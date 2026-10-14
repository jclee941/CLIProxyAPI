# SDK PACKAGE MAP

## OVERVIEW
Public v8 embedding and extension surfaces; score 14 across the subtree, with 12 immediate package directories.

Parent policy: [repository guide](../AGENTS.md).

## STRUCTURE
| Package | Role |
|---------|------|
| `access/` | Incoming request credential validation; see its AGENTS.md. |
| `api/` | Public server options and management/OAuth-session facade; protocol implementation in `api/handlers/`. |
| `auth/` | Interactive provider login and token persistence; see its AGENTS.md. |
| `cliproxy/` | Embeddable proxy service; follow `cliproxy/AGENTS.md`. |
| `config/` | Aliases and forwarding functions for `internal/config`. |
| `logging/` | Public request logger aliases and file-logger constructors. |
| `pluginabi/` | Native plugin RPC envelope, method identifiers, compatibility versions. |
| `pluginapi/` | Plugin capability interfaces and request/response contracts. |
| `pluginhost/` | Public host configuration/model conversion; follow `pluginhost/AGENTS.md`. |
| `pluginstore/` | Artifact clients and install facade; follow `pluginstore/AGENTS.md`. |
| `proxyutil/` | Shared proxy-setting parser, HTTP transports, and connection dialers. |
| `translator/` | Translation registry and middleware; `builtin/` enables built-in registrations. |

## WHERE TO LOOK
| Task | Location | Detail |
|------|----------|--------|
| Configure embedded routes | `api/options.go` | Engine hook runs before routes; router hook after routes. |
| Embed OAuth management | `api/management.go` | Handler constructors and session/callback forwarders. |
| Preserve configuration comments | `config/config.go` | Public preserve-comments save functions. |
| Embed plugin lifecycle and quota | `pluginhost/AGENTS.md` | Public host contracts and configuration ownership. |
| Fetch/install plugin artifacts | `pluginstore/AGENTS.md` | Client auth, egress identity, manifest/release entry points. |
| Interpret proxy overrides | `proxyutil/proxy.go` | Empty inherits; direct/none bypass; concrete proxy URLs select a transport. |

## CONVENTIONS
- Thin public facades use aliases and forwarding calls; implementation lives in the corresponding internal package.
- Current imports use `github.com/router-for-me/CLIProxyAPI/v8`; the `docs/sdk-*.md` guides use that path and only public `sdk/...` packages.
- Package-local checks: `go test ./sdk/<package>`; handler descendants require `go test ./sdk/api/handlers/...`.
- `proxyutil.BuildHTTPTransport` may return nil transport with ModeInherit and no error; callers retain inherited behavior.

## ANTI-PATTERNS
- Do not duplicate internal config, logging, or marketplace implementations inside their public facades.
- Do not conflate inbound API-key access (`access`) with provider login credentials (`auth`).
- Do not treat `direct`/`none` proxy settings as empty inheritance or use Parse alone to validate execution overrides; use ValidRequestProxy.
