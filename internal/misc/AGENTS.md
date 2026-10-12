# SHARED CLIENT AND CREDENTIAL HELPERS

## OVERVIEW
Client-version caching, OAuth callbacks, metadata merging, and header defaults; score 9, a distinct shared-helper domain.

## WHERE TO LOOK
| Task | File |
|------|------|
| Antigravity version cache and updater | `antigravity_version.go` |
| Version fallback and manifest regressions | `antigravity_version_test.go` |
| Callback parsing, state, and terminal prompt | `oauth.go` |
| Header source/default precedence | `header_utils.go` |
| Token-struct and custom-metadata merge | `credentials.go` |
| Metadata serialization regressions | `credentials_test.go` |
| Extension-to-MIME lookup | `mime-type.go` |
| Initial config template copy | `copy-example-config.go` |
| Embedded Claude instructions | `claude_code_instructions.go`, `claude_code_instructions.txt` |

## CONVENTIONS
- The version updater starts once per process; an RWMutex protects the cached version and expiry.
- Antigravity request helpers read cached versions instead of doing manifest network work.
- A failed refresh preserves a still-valid cache; an expired cache returns to the fallback.
- `EnsureHeader` prefers nonempty source headers, then the target value, then the default.
- `MergeMetadata` copies map input or respects JSON tags on structs; custom metadata wins collisions.
- The MIME table and embedded instructions are data assets, not generated Go entry points.

## ANTI-PATTERNS
- Do not fetch Antigravity version manifests synchronously from an executor request.
- Do not lower the fallback below the documented Cloud Code client-version floor.
- Do not assume `MergeMetadata` deep-copies nested values when its source is a map.
- Do not replace source-header precedence with an unconditional default assignment.

Package regressions: `go test ./internal/misc`.
Provider-specific OAuth clients remain under `../auth/`; payload/schema utilities remain under `../util/`.
