# AUTH CONDUCTOR KNOWLEDGE BASE

## OVERVIEW
Credential execution, scheduling, cooldown, refresh, and Home selection; score 12, a distinct runtime domain spanning 148 Go files.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Change conductor contracts | `conductor.go` | `Manager`, `ProviderExecutor`, `Selector`, `ResultPolicy` |
| Change credential state | `types.go`, `classification.go`, `status.go` | `Auth`, model state, identity, credential kind |
| Register/update/remove credentials | `conductor_lifecycle.go`, `metadata_merge.go` | Epoch checks and three-way merges |
| Execute requests or retries | `conductor_execution.go`, `conductor_stream.go` | Count/non-stream/stream paths and bootstrap errors |
| Choose candidates | `conductor_selection.go`, `scheduler.go`, `selector.go` | Shards, priority tiers, rotation, affinity |
| Change cooldown behavior | `conductor_cooldown.go`, `cooldown_state.go`, `cooldown_view.go` | Result recording, persistence, public projections |
| Interpret request-scoped failures | `conductor_request_scoped_errors.go`, `errors.go` | Configured actions and typed error boundaries |
| Refresh tokens | `conductor_refresh.go`, `auto_refresh_loop.go` | Per-auth serialization and heap scheduling |
| Resolve models/capabilities | `conductor_models.go`, `oauth_model_alias.go`, `api_key_model_capabilities.go` | Route aliases versus upstream metadata |
| Rewrite response model names | `response_model_rewriter.go` | Buffered SSE frames and raw JSON |
| Own Home attempts | `conductor_home*.go`, `home_selection.go` | Atomic dispatch bundle, retained selections, resource release |
| Publish concurrency state | `home_concurrency.go`, `home_in_flight_publisher.go` | Accounted identity checks and bounded snapshot frames |
| Merge passive quota data | `quota_signals.go` | Watermark snapshots separate from scheduling |
| Maintain session bindings | `session_cache.go`, `home_session_alias.go` | Grouped aliases, conditional invalidation, TTL |

## CONVENTIONS
- `Manager` methods are split by concern across `conductor_*.go`, not separate conductor subpackages.
- Registration epochs identify credential lifetimes; generations order snapshots within a lifetime. Scheduler removals leave tombstones.
- Prepared/refreshed auth uses base/current/updated merges so concurrent metadata edits and runtime state survive.
- Minted Meta keys are persisted under the same epoch check and installation lock before requests may use them.
- Refreshes serialize per auth ID and reuse an already-replaced token rather than refreshing it again.
- Model-level results update targeted scheduler shards; credential-level availability changes synchronize all model shards.
- Cooldown failures may extend active recovery deadlines, never shorten them.
- Passive quota observations replace the latest watermark snapshot; cooldown persistence excludes observation fields.
- Stream bootstrap inspects initial chunks before returning a stream, allowing transparent refresh/failover before downstream commitment.
- `ResultPolicy` runs before quota mutation and must support concurrent calls.
- Batch callers deferring API-key alias rebuilds must call `RefreshAPIKeyModelAlias` afterward.
- Home client, registry, and generation travel as one immutable `HomeDispatchBundle`; retained selections own session resources.

## ANTI-PATTERNS
- Do not change `Error`'s public four-field layout; causal errors use wrappers for unkeyed-literal compatibility.
- Do not treat runtime `Remove` as deletion from backing stores; callers own storage deletion.
- Do not refresh or schedule cooldowns for Home-owned credentials locally.
- Do not persist alias route-model states or recursively resolve secondary OAuth aliases.
- Do not reclassify credential/auth/quota HTTP statuses from response text.
- Do not punish the credential pool for request-scoped or pre-HTTP transport failures.
- Do not let count-token responses replace generation quota watermarks.
- Do not rotate by numeric index into shrinking candidate slices or prune weighted credits on temporary retry subsets.
- Do not mutate bindings or refresh TTL during passive affinity lookups.
- Do not reschedule disabled `invalid_grant` credentials.
- Do not resurrect removed/disabled credentials from stale scheduler snapshots.

Package check: `go test ./sdk/cliproxy/auth`; concurrency check: `go test -race ./sdk/cliproxy/auth`.
Hot regression suites: `conductor_oauth_alias_nofork_test.go`, `conductor_scheduler_*_test.go`, `home_retry_contract_test.go`.
