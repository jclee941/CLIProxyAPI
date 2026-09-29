# KIMI DEVICE FLOW

## OVERVIEW
Domain-aware Kimi device authorization and token refresh; score 11, a distinct provider-routing domain.

## WHERE TO LOOK
| Task | Location |
|------|----------|
| Domain normalization and endpoint selection | `kimi.go` |
| Device-code request and polling | `kimi.go`: `DeviceFlowClient` |
| Refresh coordination | `kimi.go`: `KimiAuth` |
| Resolve existing credential domain | `ResolveKimiDomainFromAuth` |
| Token/device response structures | `token.go` |
| Routing and cross-domain isolation | `kimi_test.go` |
| Refresh sharing | `kimi_refresh_test.go` |
| Explicit proxy precedence | `kimi_proxy_test.go` |

## CONVENTIONS
- `kimi.com` and `kimi.ai` use distinct authority/API routing.
- Use domain-aware constructors when restoring credentials or creating device clients.
- Full device-client construction can carry domain, device ID, and proxy URL together.
- Existing auth attributes/metadata determine domain through the exported resolver.
- `NormalizeKimiDomain`, `ResolveKimiOAuthHost`, and `ResolveKimiAPIBaseURL` centralize routing.
- Refresh singleflight spans separate client instances.
- Provider token storage preserves the domain information needed by downstream synthesis.

## ANTI-PATTERNS
- Do not route every Kimi credential through the default domain constructor.
- Do not mix token refreshes or API endpoints across Kimi domains.
- Do not discard a credential's explicit proxy when rebuilding its device client.
- Do not duplicate domain alias checks in runtime callers when the package resolver applies.

Run `go test ./internal/auth/kimi` for the offline suite.
Executor and watcher synthesizer consume domain resolution; check both when changing domain representation.
Use `kimi_refresh_test.go` for cross-instance refresh regressions.
