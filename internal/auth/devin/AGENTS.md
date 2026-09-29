# DEVIN AUTHENTICATION

## OVERVIEW
Devin session acquisition, profile discovery, and runtime-auth construction; score 8, a distinct provider domain.

## WHERE TO LOOK
| Task | File |
|------|------|
| Auth service and callback server | `devin_auth.go` |
| Session-token normalization | `devin_auth.go`: `FormatSessionToken` |
| Runtime credential assembly | `record.go`: `CreateAuthRecord` |
| User quota/status extraction | `user_status.go` |
| PKCE challenge generation | `pkce.go` |
| Login exchange regressions | `devin_auth_test.go` |
| Filename and quota propagation | `record_test.go` |
| Upstream status variants | `user_status_test.go` |

## CONVENTIONS
- `FormatSessionToken` supplies the `cog_` prefix when absent.
- `CreateAuthRecord` produces shared SDK auth records, not just a provider token DTO.
- Profile and status information populate identity, plan, and quota signals.
- Attribute and metadata views both carry provider-specific runtime information.
- Missing profile identity falls back to a token-derived hash to avoid overwriting another account.
- Unsafe or oversized profile identifiers become hashed file identifiers.
- The live status test is opt-in through `CPA_LIVE_TEST=true` and requires credentials.

## ANTI-PATTERNS
- Do not let upstream usernames or identifiers introduce credential path components.
- Do not substitute a shared generic filename when profile lookup is unavailable.
- Do not drop quota signals while adapting a session into `coreauth.Auth`.
- Do not treat the opt-in live status test as an offline unit test.

Offline package check: `go test ./internal/auth/devin`.
Consumers include SDK login, management OAuth handlers, and the Devin executor.
Keep service primitives here; login presentation belongs to the caller.
