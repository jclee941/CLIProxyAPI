# EXECUTION REGISTRY KNOWLEDGE BASE

## OVERVIEW
Home-lifetime pending dispatches, resource scopes, release acknowledgements, and observation barriers; score 9, a distinct concurrency domain.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Reserve or install dispatch | `registry.go` | `BeginDispatch`, `PendingDispatch.End`, `Install` |
| Bind execution resources | `registry.go` | `Scope.Bind`, `End`, `EndWithRelease` |
| Drain a subscriber lifetime | `registry.go` | Accepting/draining/closed state machine |
| Connect concurrency releases | `registry.go` | `ReleaseGroup`, `ReleaseTicket`, `SetReleaseSink` |
| Snapshot active executions | `observation.go` | `ObserveBarrier`, `FreezeInFlight` |
| Check shutdown races | `registry_test.go` | Late install/bind, pending waits, blocked closers |
| Check release delivery | `concurrency_release_test.go` | Cumulative replay and ticket acknowledgement |
| Check snapshot barriers | `observation_test.go` | Pending reservations and copied observations |

## CONVENTIONS
- Construct one registry per Home subscriber lifetime, not one registry per request.
- Reserve a pending dispatch before resolving Home credentials; install it into a scope or end the unused reservation.
- `Install` consumes the reservation atomically; reservations belong to exactly one registry.
- A scope accepts exactly one `func() error` closer. Repeated binding returns `ErrExecutionResourceAlreadyBound`.
- `End(reason)` delegates to `EndWithRelease(reason)`; scope teardown and release accounting happen once.
- Release groups are keyed by credential ID and model; sequences are cumulative, not deltas.
- Replacing a release sink replays known positive sequences outside the registry mutex.
- Legacy release callbacks cannot produce acknowledgement tickets; ticket-aware sinks return `*ReleaseTicket`.
- `WaitPending` waits on the registry change signal until every unresolved reservation is ended or installed.
- `FreezeInFlight(time.Time)` returns a copied `Freeze`, with a new snapshot revision.
- A newer observation barrier remains unpublished while a reservation at or before its captured sequence is unresolved.

## ANTI-PATTERNS
- Do not install a foreign, ended, or already-consumed reservation.
- Do not admit new dispatches or bindings after the registry leaves accepting state.
- Do not call the release sink while holding the registry mutex.
- Do not equate scope end with remote release acknowledgement; use `ReleaseTicket.Wait` where acknowledgement matters.
- Do not publish a barrier ahead of unresolved pre-barrier dispatches.
- Do not infer stable execution ordering from snapshots built from the scope map.

Package check from repository root: `go test ./sdk/cliproxy/executionregistry`.
