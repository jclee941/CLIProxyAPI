# PROVIDER AUTHENTICATION

## OVERVIEW
Provider login/refresh clients and persisted token formats; score 16, a distinct multi-provider authentication boundary.

## STRUCTURE
| Path | Provider-specific responsibility |
|------|----------------------------------|
| `models.go` | `TokenStorage.SaveTokenToFile` contract |
| `antigravity/` | Google OAuth, project discovery, and onboarding |
| `claude/` | PKCE, callback server, account identity, and OAuth transport |
| `codex/` | OpenAI PKCE, callback server, and ID-token introspection |
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

## CONVENTIONS
- Provider storage encodes provider-specific fields alongside supported custom metadata.
- Credential filename builders encode account identity rather than assuming email uniquely identifies every account.
- Test provider primitives with `go test ./internal/auth/...` from the repository root.

## ANTI-PATTERNS
- Do not restore omitted Meta credential fields from old disk metadata; cleared fields must stay cleared.
- Do not accept arbitrary discovery origins in xAI OAuth; `ValidateOAuthEndpoint` enforces HTTPS and the allowed origin.
- Do not persist empty Vertex service-account content; normalization and storage validation are separate boundaries.
