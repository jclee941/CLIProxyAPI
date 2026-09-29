# TERMINAL MANAGEMENT CLIENT

## OVERVIEW
Bubbletea management UI over the local REST API; score 8, a distinct interactive state-machine domain.

## WHERE TO LOOK
| Task | File |
|------|------|
| Root model, navigation, and runner | `app.go` |
| Management HTTP client | `client.go` |
| Overview metrics | `dashboard.go` |
| Credential operations | `auth_tab.go` |
| Key operations | `keys_tab.go` |
| Configuration editor | `config_tab.go` |
| OAuth flow and cancellation | `oauth_tab.go` |
| Log capture and presentation | `loghook.go`, `logs_tab.go` |
| Locale dictionaries and tab names | `i18n.go` |
| Layout styles | `styles.go` |

## CONVENTIONS
- Management requests use `/v0/management/` with the configured bearer secret.
- UI models do not read server internals directly.
- OAuth async messages carry a generation token matching the active flow.
- Starting or canceling a flow invalidates messages from superseded generations.
- Log polling continues while another tab is active.
- Root quit shortcuts account for text entry and the logs tab's behavior.
- `Run` accepts output and an optional embedded log hook for standalone/embedded modes.

## ANTI-PATTERNS
- Do not accept a stale OAuth start/poll message after cancellation or replacement.
- Do not close a local view while leaving its remote OAuth session active.
- Do not implement management mutation by bypassing `Client` and editing service state.
- Do not stop log polling just because the logs tab is hidden.

Run `go test ./internal/tui` for OAuth state-transition coverage.
Manual surfaces are `go run ./cmd/server --tui` and `--tui --standalone`.
