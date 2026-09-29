# CLAUDE OAUTH

## OVERVIEW
Claude OAuth, credential identity, and native transport behavior; score 8, a distinct concurrency-sensitive provider domain.

## WHERE TO LOOK
| Task | File |
|------|------|
| Authorization, profile, refresh | `anthropic_auth.go` |
| Device identity and shared metadata | `identity.go` |
| Native TLS fingerprint and session cache | `utls_transport.go` |
| Stacked response encodings | `oauth_response.go` |
| Account disambiguation and legacy migration | `filename.go` |
| Local callback lifecycle | `oauth_server.go` |
| Token serialization | `token.go` |
| PKCE verifier/challenge | `pkce.go` |

## CONVENTIONS
- `claudeDevicePoolMu` guards every access to shared credential metadata, not only device IDs.
- Use pointer-based metadata accessors when the shared map may need initialization.
- Device identity is a canonical single-element pool; accessor results are defensive copies.
- Refresh singleflight is package-wide, so separate auth client instances still deduplicate.
- TLS session caches are keyed by proxy identity and outlive per-operation auth clients.
- OAuth transport uses the captured Claude Code/Axios HTTP/1.1 profile and ordered headers.
- Companion profile/roles lookup failure does not fail the surrounding login.

## ANTI-PATTERNS
- Do not initialize or reach into shared `Auth.Metadata` outside the identity helpers.
- Do not put TLS session caches on short-lived round trippers.
- Do not move `pre_shared_key` away from the final ClientHello extension position.
- Do not remove the custom-spec resumption guard when changing uTLS extensions.
- Do not replace the OAuth wire profile with an unrelated browser profile.

Use `go test ./internal/auth/claude`; identity and refresh changes also need the race detector.
Transport regression coverage is in `utls_transport_test.go`; metadata isolation is in `identity_test.go`.
