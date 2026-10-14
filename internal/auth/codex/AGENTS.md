# CODEX OAUTH

## OVERVIEW
OpenAI/Codex PKCE login, refresh, and token introspection; score 8 (75 symbols, 44 exports, 14 importing files), a distinct OAuth domain.

## WHERE TO LOOK
| Task | File |
|------|------|
| OAuth endpoints and refresh coordination | `openai_auth.go` |
| PKCE token/bundle shapes | `openai.go` |
| Claims decoding and account lookup | `jwt_parser.go` |
| Local callback server | `oauth_server.go` |
| Callback landing pages | `html_templates.go` |
| Plan/account filename encoding | `filename.go` |
| Persisted token fields | `token.go` |
| Typed callback/auth errors | `errors.go` |
| Cryptographic PKCE construction | `pkce.go` |

## CONVENTIONS
- `NewCodexAuthWithProxyURL` provides the explicit per-credential proxy path used by runtime refresh.
- Refresh coordination is shared across auth client instances.
- `ParseJWTToken` decodes the payload only; its claims are introspection data.
- Filename generation accepts plan type, account hash, and optional provider prefix separately.
- `sdk/auth/codex.go` selects default loopback port 1455; this package accepts the caller's port.
- Keep the callback-server contract aligned with the SDK's interactive flow.

## ANTI-PATTERNS
- Do not use `ParseJWTToken` as cryptographic JWT authentication.
- Do not turn non-retryable refresh failures into unconditional retry loops.
- Do not make refresh deduplication instance-local.
- Do not collapse account-hashed credentials into an email-only filename.

Use `go test ./internal/auth/codex` for this package.
`openai_auth_test.go` covers shared refresh and cancellation behavior.
`filename_test.go` covers account/plan naming; `token_test.go` covers metadata persistence.
