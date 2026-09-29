# SIGNATURE AND REPLAY CACHES

## OVERVIEW
Provider-specific replay state with local and Home KV backends; score 11, a distinct state-consistency domain.

## WHERE TO LOOK
| Concern | File |
|---------|------|
| Signature keys, toggles, shared cleanup | `signature_cache.go` |
| Generic locked LRU | `bounded_lru.go` |
| Branch-aware signature chains | `antigravity_reasoning_replay_cache.go` |
| Cumulative Codex turn buffers | `codex_reasoning_replay_cache.go` |
| Claude signed assistant turns | `claude_thinking_replay_cache.go` |
| Kimi signed content and byte accounting | `kimi_thinking_replay_cache.go` |
| Provenance-aware Grok replay | `xai_reasoning_replay_cache.go` |

## CONVENTIONS
- Home mode selects the distributed backend exclusively; local mode uses in-process state.
- `Required` operations propagate backend errors; `BestEffort` APIs express intentionally weaker guarantees.
- Snapshot tokens are opaque optimistic-concurrency fences for conditional replace/delete.
- Antigravity tracks branch lineage and absent-key reservations in addition to revisions.
- Codex buffers include an internal turn-boundary marker for cumulative trimming.
- Claude and Kimi share snapshot machinery but retain separate cache namespaces.
- `signature_cache.go` owns one cleanup loop for the replay caches.
- Fake KV client hooks exercise distributed behavior without an external service.

## ANTI-PATTERNS
- Never fall back to local cache after a Home miss or backend failure.
- Do not swallow errors inside `Required` operations; caller policy belongs above the cache.
- Do not truncate arbitrary Antigravity chain prefixes to satisfy a size bound.
- Do not reuse stale snapshots after eviction, deletion, or branch rotation.
- Do not call the same LRU recursively from its `GetOrAdd` factory; the lock is held.
- Do not cache xAI message-only batches without replayable reasoning/tool state.

Check `go test ./internal/cache`; concurrency changes also need `go test -race ./internal/cache`.
