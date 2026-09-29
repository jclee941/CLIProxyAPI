# CLIPROXY SERVICE KNOWLEDGE BASE

## OVERVIEW
Embeddable service orchestration, reloads, and model registration; score 14, a distinct lifecycle domain above six runtime packages.

## STRUCTURE
```text
cliproxy/
|-- auth/               # Conductor, selectors, credential runtime state
|-- executionregistry/  # One Home subscriber lifetime's executions
|-- executor/           # Execution contracts, not provider implementations
|-- pipeline/           # Hook adapters and execution context
|-- session/            # Identity extraction and prefix matching
`-- usage/              # Usage delivery and token accounting contracts
```

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Construct an embedded service | `builder.go`, `types.go` | `NewBuilder`, injectable providers, hooks, watcher wrapper |
| Start or stop components | `service.go`, `service_lifecycle.go` | `Run` blocks; `Shutdown` tears down shared resources |
| Apply configuration | `service_config.go` | In-memory commit separated from runtime application |
| Synchronize credentials | `service_auth.go` | Update batching, registration ordering, stale snapshots |
| Bind provider executors | `service_executors.go` | Baseline/native/plugin bindings and refresh wrappers |
| Register model projections | `service_models.go` | Config indexes, exclusions, aliases, prefixes |
| Coordinate registration workers | `service_plugins.go` | Config API keys first; bounded category workers |
| Enrich Antigravity capabilities | `antigravity_models.go` | Async singleflight probes and TTL caches |
| Manage Home connection lifetimes | `service_home.go`, `home_plugins.go` | Supervisor, overlays, plugin staging/finalization |
| Adapt watcher hooks | `watcher.go`, `types.go` | Closure-based wrapper; `SnapshotAuths` |
| Manage auxiliary listeners | `pprof_server.go`, `discovery_advertiser.go` | Server ownership and bound endpoint identity |
| Extend execution hooks | `pipeline/context.go` | `HookFunc` adapts before/after/chunk callbacks |
| Check lifecycle regressions | `service_executionregistry_test.go`, `service_auth_sync_test.go` | Home ownership, reload ordering, registration races |

## CONVENTIONS
- Commit only in-memory configuration under `configUpdateMu`; perform runtime work under `configRuntimeMu` after checking the commit sequence.
- Install updated credentials into `coreManager` before model registration; reconcile model states and refresh the scheduler afterward.
- Persisted-auth synchronization detaches from request cancellation and uses the skip-persist context policy.
- Config API-key records retain their configuration index; duplicate key strings are not sufficient identifiers.
- Model aliases and prefixes preserve the upstream `MetadataModelID`; fork aliases retain the original catalog entry.
- An existing `excluded_models` attribute is the complete synthesized exclusion set, not an addition to global exclusions.
- `PluginMultiAuthParser` returning handled with no records intentionally suppresses built-in parsing.
- Home startup replaces local watching with subscriber-driven state; local Redis usage output is disabled in that mode.

## ANTI-PATTERNS
- Do not block config commits or targeted Management API auth updates on plugin rebuilds or capability probes.
- Do not run model-registration workers while holding `authUpdateMu`.
- Do not reject async Antigravity probe results solely because `Auth.Generation` advanced during ordinary requests.
- Do not let disabled credentials replace active executor bindings during reload.
- Do not retain catalog cooldown/suppression snapshots across deliberate remote-catalog re-registration.
- Do not restore removed watcher client-cache APIs; use auth snapshots.

Package check from repository root: `go test ./sdk/cliproxy`.
For the entire runtime subtree: `go test ./sdk/cliproxy/...`.
