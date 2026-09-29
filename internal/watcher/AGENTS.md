# CONFIG AND AUTH WATCHER

## OVERVIEW
Filesystem/config reload and ordered runtime credential updates; score 14, a distinct concurrent reconciliation domain.

## WHERE TO LOOK
| Task | Location |
|------|----------|
| Public lifecycle and update API | `watcher.go` |
| Filesystem filtering and atomic replacement | `events.go` |
| Debounced configuration reload | `config_reload.go` |
| Auth directory reload/persistence callbacks | `clients.go` |
| Snapshot reconciliation and dispatch | `dispatcher.go` |
| Human-readable change reporting | `diff/` |
| Config/file-to-auth conversion | `synthesizer/` |
| Scan/event interleaving regressions | `dispatcher_snapshot_test.go` |

## CONVENTIONS
- SHA-256 config/auth hashes suppress duplicate reload work.
- Auth updates carry watcher-local monotonic revisions, separate from runtime generation counters.
- Deleted IDs retain revision tombstones while full scans are in flight.
- A scan preserves newer event-driven state rather than replacing it with stale filesystem observations.
- Revision validation and queue insertion share the relevant synchronization boundary.
- Equality normalization removes runtime timestamps and transient fields to avoid reload loops.
- Atomic file replacement is distinguished from permanent deletion.
- Windows event paths are normalized before identity comparisons.
- Store persistence and runtime update dispatch are related but distinct operations.

## ANTI-PATTERNS
- Do not let a slow full scan resurrect an auth deleted during that scan.
- Do not discard deletion revisions merely because an auth is absent from the current map.
- Do not invalidate a valid queued update when a duplicate event is suppressed.
- Do not equate every remove/rename notification with an intentional credential delete.
- Do not compare unnormalized refresh timestamps as credential configuration changes.

Run `go test ./internal/watcher/...`; scan/dispatch changes also need race coverage.
Tests for snapshot races use controlled scan barriers; retain that synchronization contract.
