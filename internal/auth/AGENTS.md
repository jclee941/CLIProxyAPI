# PROVIDER AUTHENTICATION

## OVERVIEW
Provider login/refresh clients and persisted token formats; score 16 (subtree: 63 Go files, 9 provider packages, `claude/` has 23 importers by rg import path), a distinct multi-provider authentication boundary.
Child guides: `claude/`, `codex/`, `devin/`, `kimi/`; `antigravity/`, `meta/`, `vertex/`, `xai/`, `empty/` are covered here.

## STRUCTURE
| Path | Provider-specific responsibility |
|------|----------------------------------|
| `models.go` | `TokenStorage.SaveTokenToFile` contract |
| `antigravity/` | Google OAuth, project discovery, and onboarding |
| `claude/` | PKCE, callback server, account identity, and OAuth transport |
| `codex/` | OpenAI PKCE, SIWC, callback server, file locking, and ID-token introspection |
| `devin/` | Session token login, profile, and quota-derived auth records |
| `empty/` | No-op storage for credentials without token files |
| `kimi/` | Domain-aware device flow and refresh |
| `meta/` | Device authorization, DCA exchange, and API-key minting |
| `vertex/` | Service-account key normalization and storage |
| `xai/` | Validated OIDC discovery and device flow |

## WHERE TO LOOK
| Task | Location |
|------|----------|
| Interactive login orchestration | `../../sdk/auth/`; these packages supply provider primitives |
| CLI login selection | `../cmd/auth_manager.go` and provider login wrappers |
| Provider token schema | Provider `token.go`, or `meta/meta.go` |
| Antigravity project onboarding | `antigravity/auth.go` |
| Service-account PEM repair | `vertex/keyutil.go` |
| Discovery URL validation | `xai/xai.go` |
| Provider concurrency and wire profiles | `claude/AGENTS.md`, `codex/AGENTS.md`, `kimi/AGENTS.md` |
| Session-token adaptation | `devin/AGENTS.md` |

## CONVENTIONS
- Provider storage encodes provider-specific fields alongside supported custom metadata.
- Credential filename builders encode account identity rather than assuming email uniquely identifies every account.
- Callback ports belong to SDK orchestration; provider callback servers accept the selected port.
- `xai/` refresh singleflight is package-wide, like its guided siblings.
- Claude and Codex filename builders keep the legacy email-based format only as a fallback.
- Exception: `antigravity/filename.go` is still email-keyed (`antigravity-<email>.json`, else `antigravity.json`).
- Test provider primitives with `go test ./internal/auth/...` from the repository root.

## ANTI-PATTERNS
- Do not restore omitted Meta credential fields from old disk metadata; cleared fields must stay cleared.
- Do not accept arbitrary discovery origins in xAI OAuth; `ValidateOAuthEndpoint` enforces HTTPS and the allowed origin.
- Do not persist empty Vertex service-account content; normalization and storage validation are separate boundaries.
