# EXECUTOR KNOWLEDGE BASE

## OVERVIEW
Provider execution, native wire fidelity, replay, and stream lifecycle; score 12 (219 direct Go files, dense exports and measured LSP centrality). Also covers the 149-Go-file `helps/` package beyond the default depth-3 limit. Paths below are relative to this directory.

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
| Codex WebSocket liveness | `codex_websockets_connection.go`, `codex_websockets_session.go` | Idle deadline constant and both read paths; not the facade file |
| xAI protocol adaptation | `xai_executor_{request,response,media,speech}.go`, `xai_reasoning_replay.go` | Tool folding, search filtering, media/speech, isolated replay |
| xAI WebSocket IDs | `xai_websockets_executor.go` | Separate session store, downstream/upstream ID map, compacted transcript |
| Gemini / Vertex / AI Studio | `gemini_executor.go`, `gemini_vertex_executor.go`, `aistudio_executor.go` | Interactions, service-account/API-key paths, relay |
| Devin / Kimi / Meta | `devin_executor.go`, `helps/devin_wire.go`, `kimi_executor.go`, `kimi_thinking_replay.go`, `meta_executor*.go` | Connect-RPC framing, Claude embedding, DCA singleflight minting |
| Credential-scoped copies | `oauth_scope_executor.go` | `ForAPIKey` value receivers copy config; shared transport/session stores stay shared |
| OpenAI-compatible providers | `openai_compat_executor.go`, `openai_responses_signature.go`, `codex_openai_images.go` | Compatibility knobs, reasoning sanitization, Images adaptation |
| Usage, TTFT, served model | `helps/usage_helpers.go`, `helps/{chat,claude,gemini,responses}_ttft_helpers.go`, `helps/response_model.go` | Protocol-aware usage; requested/upstream/served models; chunk-safe observation |
| Payload-rule barrier | `helps/payload_finalizer.go`, `helps/payload_helpers.go`, `payload_barrier_test.go`, `payload_review_test.go` | Cross-provider send ordering and condition-evaluation regression guards |
| Upstream request capture | `helps/logging_helpers.go` | HTTP/WebSocket capture and error summaries; CommercialMode skips capture |
| Home refresh and identity caches | `helps/home_refresh.go`, `helps/{session_id_cache,user_id_cache}.go` | Refresh result contract and Home-aware required cache operations |
| Payload / transport utilities | `helps/payload_helpers.go`, `helps/codex_multi_agent_v2.go`, `helps/{proxy_helpers,transport_cache,utls_client}.go` | Payload rules, translation baselines, connection caches |
| Package tests | `go test ./internal/runtime/executor/...` | Includes `helps`; `testdata/tool_search_disabled/` is the Claude tool-search-disabled golden |
| Targeted race checks | `go test -race ./internal/runtime/executor -run 'TestClaudeExecutor.*SharedCredential|TestWebsocket|TestCodex.*Duplex'` | Shared auth including request preparation, session lifecycle, duplex ordering |

## CONVENTIONS
- Provider receiver methods are split by capability, not separate Go packages.
- Wire transformations favor `gjson`/`sjson` or indexed byte edits: preserve unknown fields, key order, and opaque signatures.
- Translation pairs retain an untouched baseline and an independent working buffer; plugin hooks prevent the identical-input shortcut.
- Claude sends converge on `doClaudeUpstreamRequest`; wire header casing is applied immediately before dispatch because canonical header lookup no longer finds those keys.
- Native Claude detection, cloaking, and custom-upstream policy are separate decisions; inspect the policy before changing body or headers.
- Claude CCH signs serialized bytes. Keep normalization and placeholder replacement separate from payload construction.
- Antigravity isolates transports by credential and proxy and suppresses HTTP/2; pool settings are part of the cache key.
- Codex auto routing requires both downstream WebSocket transport and credential opt-in; xAI auto routing applies that choice only to streaming requests.
- WebSocket sessions bind lifecycle and active channels to a connection, not just a session ID; stale closes must not affect replacements.
- Error scope is behavioral and declared by `IsRequestScoped`/`IsCredentialScoped` marker methods: request-scoped failures stop locally; credential-scoped failures inform rotation/cooling.
- Tests are white-box (`package executor`/`helps`) except four `helps_test` files that consume translator, registry, and plugin-host packages; `_legacy_oracle`, `_differential`, and `issue<N>` file names mark test role.
- Replay logic is provider-specific. Match session, history, and call semantics before restoring hidden state; commit only complete turns.
- xAI chat routing differs from compact/WebSocket routing: the CLI chat proxy does not implement the latter endpoints.
- xAI aliases repeated upstream response IDs with `-xai-N`; preserve the reverse mapping for `previous_response_id`.
- xAI WebSocket compaction bridges to HTTP using recorded context, validates compacted state, replaces the transcript, and emits synthetic SSE chunks.
- Devin consumes Connect-RPC frames, not SSE; clean completion requires its EOS trailer.
- `RefreshAuthViaHome` returns `handled=true` for Home failures as well as success; callers must not fall through to local refresh on that result.
- Usage records distinguish requested, upstream, and served models; additional image-tool model records leave the served-model field empty.

## ANTI-PATTERNS
### Wire and payload
- Do not cloak strongly confirmed native Claude clients merely because the operator selected `always` mode.
- Do not run full Claude Messages cloaking over `count_tokens`; its measured native body has a different shape.
- Do not sanitize schema keywords across Antigravity conversation history; restrict cleaning to schema-bearing paths.
- Do not mutate opaque thinking signatures during text obfuscation or JSON rewriting.
- Do not supplement Devin tool-result images by position; associate them with the matching tool call ID.
- Do not mix xAI internal search traces with restored client tools; filtering must not remove namespaced client calls.
- Do not route xAI compact or WebSocket requests through `xaiChatBaseURL`.

### Streams and replay
- Do not synthesize successful completion on premature EOF or read errors; preserve terminal-event success when a later downstream disconnect occurs.
- Do not infer safe Codex bootstrap buffering from missing TTFT. The closed event allowlist prevents replaying server-side tool effects.
- Preserve both bootstrap limits: 48 budget units and 1 MiB retained bytes; SSE charges lines, WebSocket charges messages read.
- Do not fall back to HTTP when the execution context requires upstream WebSocket replay.
- Do not hold Codex WebSocket session locks across account checks; they may call back into the auth manager.
- Do not notify downstream disconnect on a retryable Codex bootstrap overload teardown.
- Do not consume duplex follow-ups until the initial bootstrap succeeds.
- Do not enable Codex or xAI outbound WebSocket write compression; negotiating `permessage-deflate` is permitted.
- Do not acquire the xAI session write mutex for ping-handler pongs; concurrent `WriteControl` avoids starvation during uploads.
- Do not inject xAI reasoning cache items into incremental WebSocket turns already using `previous_response_id`.
- Do not share an Antigravity replay index across goroutines or retain it after its payload changes.

### Helpers and regression tests
- Keep frozen Antigravity legacy oracles independent of production; assert intentional new behavior separately, not by rewriting the oracle.
- Do not parallelize tests that replace process-global logrus hooks and model-substitution throttles (`helps/response_model_test.go`).
- Do not reenter `TransportCache.Get` from its build callback; the cache lock is held.
- Do not share TLS resumption state across proxy identities; keep `pre_shared_key` last in the Claude ClientHello extension list.
- For Claude fingerprint changes, inspect `helps/utls_client_test.go` and native-capture fixtures together with header tests.
