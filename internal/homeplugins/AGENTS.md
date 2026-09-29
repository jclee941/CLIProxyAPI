# HOME PLUGIN SYNC KNOWLEDGE BASE

## OVERVIEW
Home-directed plugin artifact reconciliation and install/load reporting; score 11, a distinct synchronization domain.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Config-driven sync | `sync.go: SyncPlatformWithReport` | Enabled plugin manifests and target platform |
| Home-resolved sync | `sync.go: SyncResolvedWithReport` | Temporary auth and response expiry |
| Installed inventory | `sync.go: InstalledVersions` | Platform-aware file selection |
| Install coordination | `sync.go: installManifest` | Busy-plugin checks and unload handoff |
| Delete tasks | `sync.go: DeleteWithReport` | Current-platform artifact deletion/reporting |
| Runtime load confirmation | `sync.go: MarkLoadResults` | Distinguish install success from registration |
| Report construction | `sync.go: newSyncReport`, `finishReport` | Task phase, node, timestamps, aggregate status |
| Fake runtime and HTTP coverage | `sync_test.go` | Download, lock, delete, temporary-auth scenarios |
| Shared egress identity | `network_scope_test.go` | Proxy cooldown sharing across clients |

## CONVENTIONS
- All implementation currently lives in `sync.go`; exported entry points share its report machinery.
- Separate artifact installation results from runtime load results.
- Aggregate individual install failures while retaining per-plugin status records.
- Clear resolved auth after each item and on every function exit, including early returns.
- Validate resolved response expiry before consuming each item.
- Prefer `<root>/<goos>/<goarch>/` artifacts over root-level fallback files.
- Use the normalized platform for extensions, inventory, install, and deletion.
- Preserve installed-version status for items omitted from a resolved download batch.
- Prefer contextual unloading when the supplied runtime supports it.
- Run `go test -race ./internal/homeplugins` for reconciliation checks.

## ANTI-PATTERNS
- Do not report a downloaded artifact as loaded before runtime registration is inspected.
- Do not delete a busy artifact when unloading fails or it remains busy afterward.
- Do not retain resolved authentication material for later synchronization attempts.
- Do not erase earlier per-plugin failures when composing the final report.
- Do not accept empty, dot, dot-dot, or separator-containing plugin IDs.
- Do not include the filename's leading version marker in normalized version values.
- Do not delete another platform's artifact while processing the current node's task.
