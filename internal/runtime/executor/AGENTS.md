# EXECUTOR KNOWLEDGE BASE

## OVERVIEW
Provider execution, native wire fidelity, replay, and stream lifecycle; score 12, distinct domain with 166 direct Go files. Covers `helps/` at the depth limit.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Claude request dispatch | `claude_executor_{execute,stream,request}.go` | Shared send boundary, error scopes, MCP alias restoration |
| Claude identity and signing | `claude_fingerprint_policy.go`, `claude_signing.go`, `claude_executor_cloaking.go` | Native detection, byte-sensitive CCH, prompt cache placement |
| Claude credential preparation | `claude_executor_auth.go`, `helps/claude_credential_identity.go` | Profile/device identity and shared-auth synchronization |
| Antigravity transport and quota | `antigravity_executor.go`, `antigravity_executor_{auth,credits,request}.go` | Credential-isolated HTTP/1.1, refresh, schema paths |
| Antigravity replay | `antigravity_reasoning_replay.go`, `antigravity_reasoning_replay_index_test.go` | Indexed history matching and independent differential oracle |
| Codex HTTP and terminals | `codex_executor_{execute,stream,terminal}.go` | Bootstrap failover, output reconstruction, terminal errors |
| Codex WebSocket lifecycle | `codex_websockets_{executor,session,duplex,stream}.go` | Auto transport selection, connection ownership, queued creates |
| xAI protocol adaptation | `xai_executor_{request,response,media}.go`, `xai_reasoning_replay.go` | Tool folding, search filtering, media, isolated replay |
| xAI WebSocket IDs | `xai_websockets_executor.go` | Separate session store, downstream/upstream ID map, compacted transcript |
| Gemini / Vertex / AI Studio | `gemini_executor.go`, `gemini_vertex_executor.go`, `aistudio_executor.go` | Interactions, service-account/API-key paths, relay |
| Devin / Kimi / Meta | `devin_executor.go`, `kimi_executor.go`, `meta_executor*.go` | Connect-RPC, Claude delegation, temporary DCA credentials |
| OpenAI-compatible providers | `openai_compat_executor.go`, `openai_responses_signature.go`, `codex_openai_images.go` | Compatibility knobs, reasoning sanitization, Images adaptation |
| Usage and token timing | `helps/usage_helpers.go`, `helps/*ttft_helpers.go`, `helps/stream_response_model_observer.go` | Protocol-aware usage and chunk-safe model observation |
| Payload / transport utilities | `helps/payload_helpers.go`, `helps/codex_multi_agent_v2.go`, `helps/{proxy_helpers,transport_cache,utls_client}.go` | Payload rules, translation baselines, connection caches |
| Package tests | `go test ./internal/runtime/executor/...` | Includes `helps`; run from repository root |
| Targeted race checks | `go test -race ./internal/runtime/executor -run 'TestClaudeExecutorSharedCredential|TestWebsocket'` | Shared auth and session lifecycle |

## CONVENTIONS
- Provider receiver methods are split by capability, not separate Go packages.
- Follow the root executor/helps placement rule when adding files; existing colocated support files are not a precedent.
- Wire transformations favor `gjson`/`sjson` or indexed byte edits: preserve unknown fields, key order, and opaque signatures.
- Translation pairs retain an untouched baseline and an independent working buffer; plugin hooks prevent the identical-input shortcut.
- Claude sends converge on `doClaudeUpstreamRequest`; wire header casing is applied immediately before dispatch because canonical header lookup no longer finds those keys.
- Native Claude detection, cloaking, and custom-upstream policy are separate decisions; inspect the policy before changing body or headers.
- Claude CCH signs serialized bytes. Keep normalization and placeholder replacement separate from payload construction.
- Antigravity isolates transports by credential and proxy and suppresses HTTP/2; pool settings are part of the cache key.
- Codex auto routing requires both downstream WebSocket transport and credential opt-in; xAI auto routing applies that choice only to streaming requests.
- WebSocket sessions bind lifecycle and active channels to a connection, not just a session ID; stale closes must not affect replacements.
- Error scope is behavioral: request-scoped failures stop locally; credential-scoped failures inform rotation/cooling.
- Replay logic is provider-specific. Match session, history, and call semantics before restoring hidden state; commit only complete turns.
- xAI chat routing differs from compact/WebSocket routing: the CLI chat proxy does not implement the latter endpoints.
- xAI aliases repeated upstream response IDs with `-xai-N`; preserve the reverse mapping for `previous_response_id`.
- xAI WebSocket compaction bridges to HTTP using recorded context, validates compacted state, replaces the transcript, and emits synthetic SSE chunks.
- Devin consumes Connect-RPC frames, not SSE; clean completion requires its EOS trailer.

## ANTI-PATTERNS
### Wire and payload
- Do not cloak strongly confirmed native Claude clients merely because the operator selected `always` mode.
- Do not run full Claude Messages cloaking over `count_tokens`; its measured native body has a different shape.
- Do not sanitize schema keywords across Antigravity conversation history; restrict cleaning to schema-bearing paths.
- Do not mutate opaque thinking signatures during text obfuscation or JSON rewriting.
- Do not mix xAI internal search traces with restored client tools; filtering must not remove namespaced client calls.
- Do not route xAI compact or WebSocket requests through `xaiChatBaseURL`.

### Streams and replay
- Do not infer safe Codex bootstrap buffering from missing TTFT. The closed event allowlist prevents replaying server-side tool effects.
- Preserve both bootstrap limits: 48 budget units and 1 MiB retained bytes; SSE charges lines, WebSocket charges messages read.
- Do not fall back to HTTP when the execution context requires upstream WebSocket replay.
- Do not notify downstream disconnect on a retryable Codex bootstrap overload teardown.
- Do not consume duplex follow-ups until the initial bootstrap succeeds.
- Do not enable Codex or xAI outbound WebSocket compression; negotiated compression and write compression are separate.
- Do not acquire the xAI session write mutex for ping-handler pongs; concurrent `WriteControl` avoids starvation during uploads.
- Do not inject xAI reasoning cache items into incremental WebSocket turns already using `previous_response_id`.
- Do not share an Antigravity replay index across goroutines or retain it after its payload changes.

### Helpers and regression tests
- Keep frozen Antigravity legacy oracles independent of production; assert intentional new behavior separately, not by rewriting the oracle.
- Do not parallelize tests that replace process-global logrus hooks and model-substitution throttles.
- Do not reenter `TransportCache.Get` from its build callback; the cache lock is held.
- Do not share TLS resumption state across proxy identities; keep `pre_shared_key` last in the Claude ClientHello extension list.
- For Claude fingerprint changes, inspect `helps/utls_client_test.go` and native-capture fixtures together with header tests.
