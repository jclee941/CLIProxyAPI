# COMMAND ENTRY POINTS

## OVERVIEW
Standalone executables and startup orchestration; score 9 from file count, directory breadth, code ratio, and LSP symbol density.

## STRUCTURE
- `server/`: proxy startup, command dispatch, discovery, TUI, plugin bootstrap.
- `fetch_codex_models/`: authenticated Codex catalog extraction.
- `fetch_antigravity_models/`: endpoint fallback and model extraction.
- `fetch_devin_models/`: Connect-RPC catalog extraction without generated protobufs.
- `validate_codex_models/`: catalog validation used by CI refresh scripts.
- `telegram-usage-bot/`: retained usage-reporting executable.
- `telegram-log-forwarder/`: retained error-log delivery executable.

## WHERE TO LOOK
| Task | Location | Detail |
| --- | --- | --- |
| Flags and startup modes | `server/main.go` | `main`, `argvEnablesBoolFlag`, `splitArgvFlag` |
| Default-key startup gate | `server/main.go` | `shouldEnableExampleAPIKeySafeMode` |
| Local catalog mode | `server/main.go` | `localModel` flag gates `registry.SetLocalModelCatalogs` beside the auto-updaters (two startup branches) |
| Pre-start plugin config | `server/main.go` | `pluginBootstrapConfigPath`, `loadPluginBootstrapConfig` |
| Management URL resolution | `server/main.go` | `resolveManagementBaseURL` |
| Startup regression cases | `server/main_test.go` | Bool-flag argv parsing, safe mode, Home port, management base URL |
| Devin wire decoding | `fetch_devin_models/main.go` | `protowire` parsing and model aggregation |
| Error grouping | `telegram-log-forwarder/state.go` | Bounded groups and persisted delivery state |
| Usage schedules and offsets | `telegram-usage-bot/bot.go`, `telegram-usage-bot/state.go` | Report dispatch and restart progress |

## CONVENTIONS
- Every directory is `package main`; these are executable boundaries, not importable SDK packages.
- Build metadata enters `server` through `-ldflags -X main.Version`, `main.Commit`, and `main.BuildDate`.
- Discovery has an early subcommand path and legacy boolean flags; JSON discovery must keep stdout machine-readable.
- Safe-mode decisions distinguish server, command, Home, cloud standby, and non-standalone TUI modes.
- Fetchers write catalogs; `validate_codex_models` validates the resulting file rather than contacting a provider.
- CI catalog refresh (`../.github/scripts/refresh-model-catalogs.sh`) validates the remote Codex candidate with `go run ./cmd/validate_codex_models --file` and keeps the embedded catalog on failure.
- Telegram commands keep one shared `test_helpers_test.go` per directory; test names follow `Test<Subject>_<behavior>_when<Condition>`.
- Run command tests from the repository root with `go test ./cmd/...`.
- Inspect `../deploy/AGENTS.md` before treating retained Telegram commands as deployed services.
- Shared coding rules live in `../AGENTS.md`; startup implementation helpers live under `../internal/cmd/`.

## ANTI-PATTERNS
- Do not move discovery interception after the normal startup banner.
- Do not bypass example-key safe mode to start a live proxy with sample credentials.
- Do not require a local config file in startup paths that intentionally load optional Home configuration.
- Do not confuse the in-memory Redis-compatible usage queue with an external Redis startup dependency.
- Do not replace Devin's wire parser with assumptions about JSON responses.
