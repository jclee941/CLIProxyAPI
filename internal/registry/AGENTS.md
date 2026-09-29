# MODEL REGISTRY

## OVERVIEW
Live model availability, capability projections, and embedded/remote catalogs; score 14, a central routing metadata domain.

## WHERE TO LOOK
| Task | File |
|------|------|
| Client registration and availability | `model_registry.go` |
| Static provider catalogs and builtins | `model_definitions.go` |
| Main catalog refresh | `model_updater.go` |
| Codex immutable snapshots/revision | `codex_client_models.go` |
| Codex remote refresh | `codex_client_models_updater.go` |
| Devin namespacing and catalog parsing | `devin_models.go` |
| Devin remote refresh | `devin_models_updater.go` |
| Embedded fallback assets | `models/*.json` |

## CONVENTIONS
- Query APIs return defensive clones, including nested metadata and response maps.
- Registration epochs track structural bindings; generation tracks availability changes.
- Client model projections reject stale epochs/generations and unregistered bindings.
- Re-registering a binding clears its transient quota/suspension scheduling state.
- Capability mutation uses an expected client epoch to avoid updating a replacement binding.
- Web-search capability is tri-state; route aggregation is conservative.
- Validate downloaded catalogs before swapping the current snapshot.
- Builtin overlays preserve required models even when remote catalogs omit them.
- Hooks run asynchronously outside the registry's critical path.

## ANTI-PATTERNS
- Do not expose internal model fields such as metadata model ID, compat flags, or native capabilities in ordinary listings.
- Do not hand callers internal maps/slices or retain stale capability snapshots after re-registration.
- Do not confuse a client registration epoch with the global availability generation.
- Do not replace a valid catalog with a malformed download.
- Do not remove the required Codex fallback template from catalog validation.
- Hook implementations must remain non-blocking despite asynchronous dispatch.

Run `go test ./internal/registry` for clone, projection, quota, and catalog regressions.
Catalog automation is `.github/scripts/refresh-model-catalogs.sh`; validators live under `cmd/`.
