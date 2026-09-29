# PLUGIN STORE KNOWLEDGE BASE

## OVERVIEW
Registry validation, authenticated artifact retrieval, and safe binary installation; score 11, a distinct distribution boundary.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Catalog schemas | `registry.go` | V1/V2 parsing, source normalization, install plans |
| Pinned installation data | `manifest.go` | Manifest construction and validation |
| GitHub discovery/downloads | `github.go` | Releases, tags, assets, archives |
| Direct artifacts | `direct.go` | Platform selection and SHA-256 validation |
| Archive installation | `install.go` | Root-only binary, atomic replacement, loaded-file guard |
| Checksum manifests | `checksum.go` | Parse and verify checksums |
| Auth matching | `auth.go` | Request kind, URL scope, redirects, clearable secrets |
| Request/cache identity | `request_identity.go` | Credential snapshot plus network scope |
| GitHub cooldowns | `github_rate_limit.go` | Shared limiter and Retry-After handling |
| Home exchange contract | `home_sync.go` | Resolved auth, expiry, schema validation |
| Upgrade comparison | `version.go` | Dotted numeric version handling |

## CONVENTIONS
- GitHub-release and direct installs converge on validated manifests and platform artifacts.
- PrepareLatestRelease binds cache identity and request headers to one credential snapshot.
- Cache/rate-limit identity includes network scope and effective authentication headers.
- Registry, metadata, and artifact requests have separate auth kinds.
- Temporary resolved credentials use byte-backed `Secret` values with explicit clearing.
- Archive installation accepts one correctly named dynamic library at the archive root.
- Preserve the difference between release tags and normalized plugin versions.
- Run `go test ./internal/pluginstore` for catalog, auth, throttle, and archive checks.

## ANTI-PATTERNS
- Do not accept URL userinfo as plugin-store authentication.
- Do not put credentials, query strings, or fragments in pinned artifact URLs.
- Do not allow ZIP traversal, backslash separators, nonregular entries, or nested target libraries.
- Do not bypass `ErrLoadedPluginLocked` when an artifact is still in use.
- Do not let a late successful request clear another request's established cooldown.
- Do not round Retry-After down and retry before the reset time.
- Do not read a rate-limited response body merely to construct an error message.
- Do not key release caches solely by repository while credentials or egress scopes differ.
