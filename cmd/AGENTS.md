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
| Catalog refresh selection | `server/main.go` | `modelCatalogUpdaterPlan` |
| Pre-start plugin config | `server/main.go` | `loadPluginBootstrapConfig` |
| Startup regression cases | `server/main_test.go` | Flags, safe mode, Home port, updater plan |
| Devin wire decoding | `fetch_devin_models/main.go` | `protowire` parsing and model aggregation |
| Error grouping | `telegram-log-forwarder/state.go` | Bounded groups and persisted delivery state |
| Usage schedules and offsets | `telegram-usage-bot/bot.go`, `state.go` | Report dispatch and restart progress |

## CONVENTIONS
- Every directory is `package main`; these are executable boundaries, not importable SDK packages.
- Build metadata enters `server` through `-ldflags -X main.Version`, `main.Commit`, and `main.BuildDate`.
- Discovery has an early subcommand path and legacy boolean flags; JSON discovery must keep stdout machine-readable.
- Safe-mode decisions distinguish server, command, Home, cloud standby, and non-standalone TUI modes.
- Fetchers write catalogs; `validate_codex_models` validates the resulting file rather than contacting a provider.
- Run command tests from the repository root with `go test ./cmd/...`.
- Inspect `../deploy/AGENTS.md` before treating retained Telegram commands as deployed services.

## ANTI-PATTERNS
- Do not move discovery interception after the normal startup banner.
- Do not bypass example-key safe mode to start a live proxy with sample credentials.
- Do not require a local config file in startup paths that intentionally load optional Home configuration.
- Do not retain the startup Redis probe connection after its verification step.
- Do not replace Devin's wire parser with assumptions about JSON responses.
