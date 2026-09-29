# REQUEST ACCESS AUTHENTICATION

## OVERVIEW
Ordered frontend credential-provider registry and authentication coordinator; score 8, distinct inbound-access domain.

## WHERE TO LOOK
| Task | Location | Detail |
|------|----------|--------|
| Validate an incoming request | `manager.go` | Manager.Authenticate evaluates a provider snapshot. |
| Configure active providers | `manager.go` | SetProviders replaces a manager's list. |
| Register global providers | `registry.go` | Pre-built Provider instances keyed by trimmed type identifiers. |
| Restrict provider visibility | `registry.go` | SetExclusiveProvider and ClearExclusiveProvider. |
| Classify authentication failure | `errors.go` | AuthError codes, constructors, HTTPStatusCode, and unwrap support. |
| Define inline API keys | `types.go` | AccessConfig, AccessProvider, MakeInlineAPIKeyProvider. |
| Check registry behavior | `registry_test.go` | Exclusive selection, restoration, stale key handling. |

## CONVENTIONS
- Provider.Authenticate returns Result plus *AuthError, not an unclassified error.
- Registration order determines RegisteredProviders order; replacing a key preserves its position.
- Exclusivity applies only while the selected key has a registered, non-nil provider.
- A stale exclusive key falls back to the ordinary registered list.
- Manager provider slices are copied on both SetProviders and Providers.
- The global registry and each Manager's active list are separate state.
- First successful provider ends authentication immediately.
- NotHandled continues; NoCredentials and InvalidCredential accumulate while later providers are tried.
- Other authentication errors stop evaluation immediately.
- With no success, invalid credentials take precedence over missing credentials.
- A nil manager or empty active list returns nil result and nil error.
- Focused validation: `go test ./sdk/access`.

## ANTI-PATTERNS
- Do not treat an empty provider list as an authentication rejection; it represents no configured enforcement here.
- Do not expect SetExclusiveProvider to delete providers or alter an existing Manager snapshot.
- Do not leak global registration or exclusive-provider state between tests.
- Do not return InvalidCredential merely because a provider does not handle the credential type; use NotHandled.
