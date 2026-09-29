# SESSION IDENTITY KNOWLEDGE BASE

## OVERVIEW
Multi-harness identity extraction, deterministic session derivation, and Merkle prefix affinity; score 9, a distinct protocol-parsing domain.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Extract explicit identities | `info.go` | `ExtractSessionInfo`, `BoundSessionIdentity` |
| Index request identity fields | `info.go` | `sessionObject` caches keys and duplicate detection |
| Reconcile metadata | `identity.go` | `Enrich`, canonical UUID projection, caller scope |
| Derive fallback identity | `identity.go` | `DeriveID`; `DerivedID` reads stored metadata |
| Normalize conversation turns | `lcp.go` | `ExtractCanonicalTurns`, fingerprints, protocol adapters |
| Match prefixes/compaction | `lcp.go` | `MerklePrefixMatcher`, bounded LRU namespaces |
| Inspect compatibility shims | `tree_compat.go` | Deprecated session-tree surface |
| Check harness precedence | `info_test.go`, `info_duplicate_test.go` | Competing identifiers and duplicate JSON keys |
| Check parsing costs | `info_performance_test.go` | Large payloads with absent candidate fields |
| Check identity stability | `identity_test.go` | Conversation growth and payload immutability |
| Check expiration and lineage | `lcp_test.go`, `lcp_lookup_test.go` | Mock-clock TTL, forks, sliding windows |

## CONVENTIONS
- `CallerScope(string)` hashes the caller partition; inferred identities keep downstream callers isolated.
- `Enrich` writes consistent identity metadata to both request and options; explicit identities take precedence over fallback derivation.
- If original request bytes are absent, `Enrich` borrows `req.Payload` as a read-only baseline instead of copying it.
- Missing top-level identity candidates use the cached JSON index, not repeated scans of message arrays.
- Canonical turns normalize protocol-specific conversation representations before prefix matching.
- A prefix needs a user or assistant turn; system-only prefixes must not establish conversation affinity.
- Large parts retain full-content SHA-256 identity with bounded retained fingerprint bytes.
- Prefix matcher expiration and eviction run inline; the matcher starts no cleanup goroutine.
- `MerklePrefixMatcherConfig.NowFunc` is the clock seam for expiration tests.
- `Clear` empties bindings but preserves the monotonic access generation.

## ANTI-PATTERNS
- Do not mutate borrowed payload bytes after `Enrich` without supplying an independent `OriginalRequest` first.
- Do not reset `accessCounter` during clear; pre-clear work must not evict newer bindings.
- Do not let delayed successful requests overwrite a binding already changed by failover.
- Do not turn missing-key lookup into repeated full-payload parsing.
- Do not expand deprecated local session-tree ownership; multi-node lineage belongs to Home.
- Do not treat transitional legacy-prefix stripping as the permanent ingress identity format.

Package check: `go test ./sdk/cliproxy/session`.
Matching benchmark: `go test -run='^$' -bench=BenchmarkMerklePrefixMatcherMatch -benchmem ./sdk/cliproxy/session`.
