# TERMINAL MANAGEMENT CLIENT

## OVERVIEW
Bubbletea management UI over REST; score 9, a distinct interactive state-machine domain.

## WHERE TO LOOK
| Task | File |
|------|------|
| Root model, navigation, and runner | `app.go` |
| Management HTTP client | `client.go` |
| Remote base URL and bearer handling | `client_test.go` |
| Overview metrics | `dashboard.go` |
| Credential operations | `auth_tab.go` |
| Key operations | `keys_tab.go` |
| Configuration editor | `config_tab.go` |
| OAuth flow and cancellation | `oauth_tab.go` |
| Platform browser commands | `browser.go` |
| Log capture and presentation | `loghook.go`, `logs_tab.go` |
| Locale dictionaries and tab names | `i18n.go` |
| Layout styles | `styles.go` |

## CONVENTIONS
- `NewClient` targets localhost; `NewClientWithBaseURL` supports a configured HTTP(S) origin.
- Client methods still use `/v0/management` paths and attach the configured bearer secret.
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
