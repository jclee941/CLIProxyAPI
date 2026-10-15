# CODEX OAUTH

## OVERVIEW
OpenAI/Codex PKCE and Sign in with ChatGPT (SIWC) login, refresh, credential-file locking, and token introspection; score 9 (19 Go files, 22 importing files by rg import path), a distinct OAuth domain.

## WHERE TO LOOK
| Task | File |
|------|------|
| OAuth endpoints and refresh coordination | `openai_auth.go` |
| PKCE token/bundle shapes | `openai.go` |
| Claims decoding and account lookup | `jwt_parser.go` |
| Local callback server | `oauth_server.go` |
| Callback landing pages | `html_templates.go` |
| Plan/account filename encoding | `filename.go` |
| Persisted token fields, file lock, atomic write, disk merge | `token.go` |
| SIWC authorization, exchange, refresh | `siwc_auth.go` |
| Partition-scoped refresh results | `refresh_patch.go` |
| Inter-process lockfile per platform | `filelock_unix.go`, `filelock_windows.go` |
| Typed callback/auth errors | `errors.go` |
| Cryptographic PKCE construction | `pkce.go` |

## CONVENTIONS
- `NewCodexAuthWithProxyURL` provides the explicit per-credential proxy path used by runtime refresh.
- Refresh coordination is shared across auth client instances.
- `ParseJWTToken` decodes the payload only; its claims are introspection data.
- Filename generation accepts plan type, account hash, and optional provider prefix separately.
- `sdk/auth/codex.go` selects default loopback port 1455; this package accepts the caller's port.
- Keep the callback-server contract aligned with the SDK's interactive flow.
- Credential files carry separate outer and `siwc` partitions plus a `use_siwc` switch.
- `WithFileLock` pairs an in-process mutex with an inter-process lockfile; `WriteJSONFileAtomic` replaces files.
- `CredentialRefreshPatch` applies only refreshed partitions whose disk version still matches.
- `MergeCodexCredentialsFromDisk` keeps disk credentials during configuration saves.

## ANTI-PATTERNS
- Do not use `ParseJWTToken` as cryptographic JWT authentication.
- Do not turn non-retryable refresh failures into unconditional retry loops.
- Do not make refresh deduplication instance-local.
- Do not collapse account-hashed credentials into an email-only filename.
- Do not overwrite a partition rotated on disk since refresh began; go through `CredentialRefreshPatch`.

Use `go test ./internal/auth/codex` for this package.
`openai_auth_test.go` covers shared refresh and cancellation behavior.
`filename_test.go` covers account/plan naming; `token_test.go` covers metadata persistence.
`siwc_auth_test.go` and `refresh_patch_test.go` cover SIWC exchange and partitioned refresh merges.
