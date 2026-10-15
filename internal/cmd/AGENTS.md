# CLI ORCHESTRATION

## OVERVIEW
Command implementations behind `../../cmd/server/`; score 9, a distinct startup/login/discovery domain.

## WHERE TO LOOK
| Task | File |
|------|------|
| Foreground/background service lifecycle | `run.go` |
| Authenticator construction | `auth_manager.go` |
| Credential status table | `auth_status.go` |
| LAN scan and display | `discover.go` |
| Provider login options | `openai_login.go` |
| Browser-based Codex login | `openai_login.go` |
| Device-code Codex login | `openai_device_login.go` |
| SIWC bind into an existing Codex credential | `siwc_bind.go` |
| Other provider login wrappers | `*_login.go` |
| Vertex JSON import | `vertex_import.go` |
| Interactive prompt and EOF handling | `login_prompt.go` |

## CONVENTIONS
- Executable flag parsing lives in `../../cmd/server/main.go`; this package handles command execution.
- Login wrappers share `LoginOptions` for browser, callback-port, and prompt behavior.
- `newAuthManager` centralizes SDK authenticator registration.
- Background service startup returns cancellation and a completion channel.
- `DoSIWCBind` writes the `siwc` section and `use_siwc=true` into a file resolved by `ResolveSIWCBindPath`.
- Cloud standby is a distinct lifecycle path when deployment configuration is absent.
- Discovery CLI interface filters override configured filters.
- Discovery config extraction reads only the scan-related subset.
- LAN discovery output strips terminal-control and invisible formatting characters.
- Vertex prefixes reject internal `/`; `sanitizeFilePart` replaces both slash forms in filenames.

## ANTI-PATTERNS
- Do not let Vertex import prefixes introduce path separators.
- Do not print raw discovered names directly to the terminal; retain display sanitization.
- Do not rebuild provider OAuth exchanges here; call SDK authenticators.
- Existing callback-port conflict exits are CLI-specific behavior, not a pattern for reusable packages.

Run `go test ./internal/cmd`; discovery behavior has dedicated coverage in `discover_test.go`.
