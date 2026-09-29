# USAGE CONTRACT KNOWLEDGE BASE

## OVERVIEW
Asynchronous usage-plugin delivery and provider-aware token breakdowns; score 9, a distinct telemetry contract domain.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Change usage event fields | `manager.go` | `Record`, `Detail`, `Failure` |
| Carry request attribution | `manager.go` | Request/trace ID, alias, effort, tier, generate/stream context helpers |
| Register consumers | `manager.go` | `Register`, `RegisterNamed`, default-manager wrappers |
| Change queue delivery | `manager.go` | `Publish`, `run`, `dispatch`, `safeInvoke` |
| Interpret provider tokens | `accounting.go` | Subset, independent, separate-reasoning semantics |
| Validate breakdowns | `accounting.go` | Schema, quality, overflow-safe totals |
| Check context contracts | `manager_test.go` | Flags and record fields |
| Check token arithmetic | `accounting_test.go` | Cache/reasoning relationships and inconsistent counts |

## CONVENTIONS
- The queue is a mutex/condition-variable slice, not a bounded channel; `NewManager(buffer int)` currently does not enforce the supplied capacity.
- `Publish` lazily starts the worker and fills missing request/trace IDs from context before enqueueing.
- The worker delivers queued records serially; dispatch snapshots the plugin list before callbacks.
- Named registration replaces an existing plugin slot rather than appending duplicate consumers.
- `Stop` marks the queue closed and wakes the worker to drain remaining records; it does not wait for callback completion.
- Manager start/stop are one-shot operations; a stopped manager does not restart.
- Plugin panics are recovered at `safeInvoke`, isolating delivery to remaining consumers.
- `EnsureTokenBreakdownForProvider(detail, provider, executorType)` returns a normalized `Detail`.
- OpenAI-compatible accounting treats cache/reasoning as subsets; Claude cache tokens are independent; Gemini-family reasoning is separate.
- Explicit OpenAI-compat executor semantics take precedence over provider-name substring classification.
- Unknown token semantics remain unclassified; contradictory or overflowing counts produce inconsistent quality.

## ANTI-PATTERNS
- Do not assume the constructor's buffer argument bounds memory or provides producer backpressure.
- Do not treat `Stop` as a synchronous delivery acknowledgement.
- Do not block usage callbacks indefinitely: a slow plugin stalls the single delivery worker.
- Do not sum cache or reasoning buckets identically across providers; use the accounting constructors.
- Do not convert invalid arithmetic into apparently complete accounting.
- Do not emit deprecated `RequestServiceTier`; it is an input-only compatibility alias for `ServiceTier`.

Package check from repository root: `go test ./sdk/cliproxy/usage`.
