# PROTOCOL HANDLERS

## OVERVIEW
Shared authenticated execution and Claude/Gemini/OpenAI HTTP, SSE, and WebSocket surfaces; score 14, depth-3 guide covering protocol descendants.

## WHERE TO LOOK
| Task | Location | Detail |
|------|----------|--------|
| Context, cancellation, keep-alives | `handlers.go`, `handlers_context.go` | BaseAPIHandler and request-scoped routing/auth metadata. |
| Non-streaming execution | `handlers_execution.go` | Auth manager dispatch, plugin executor dispatch, lifecycle completion. |
| Streaming bootstrap/retry | `handlers_stream.go` | First-payload validation, retry budget, cloned header snapshots. |
| Plugin hooks and routing | `handlers_interceptors.go`, `handlers_routing.go` | Before/after auth, response/chunk interception, model routing. |
| Nested host execution | `model_execution.go` | ExecuteModel/ExecuteModelStream and explicit protocol requests. |
| Error/header handling | `handlers_errors.go`, `header_filter.go` | HTTP envelopes and reserved/upstream header separation. |
| SSE forwarding | `stream_forwarder.go` | Central flushing, completion/error callbacks, channel closure. |
| Claude protocol | `claude/code_handlers.go` | Messages, count tokens, model-list mapping, gzip sniffing. |
| Gemini protocol | `gemini/gemini_handlers.go`, `gemini/interactions_handlers.go` | Native generation and explicit interaction operations. |
| OpenAI REST/media | `openai/openai_handlers.go`, `openai/openai_images_handlers.go`, `openai/openai_videos_handlers.go` | Chat/completions, image routes, async video auth binding. |
| Responses SSE | `openai/openai_responses_handlers.go` | Frame repair, terminal events, compact endpoint. |
| Responses WebSocket | `openai/openai_responses_websocket*.go` | Connection lifecycle, transcripts, steering, tool repair, timeline. |

## CONVENTIONS
- Protocol handlers embed BaseAPIHandler; execution uses its auth-manager entry points.
- Delay SSE status/header commitment until startup errors can still become ordinary HTTP errors.
- ForwardStream owns flushing; callbacks write protocol payloads only.
- Stop the non-streaming keep-alive function before writing the final response.
- Nested plugin model execution skips the caller's router/interceptors, not every plugin.
- Nested execution invalidates outer prepared routes that could point back to the caller.
- Preserve original requested-model metadata separately from the normalized execution model.
- Internal model calls receive filtered upstream headers even when public passthrough is configured.
- Video retrieval/content requests reuse the creator auth/model binding; bindings expire after 24 hours.
- Gemini interaction creation requires exactly one of model or agent; retrieval uses interaction.get, not create replay.
- Focused checks: `go test ./sdk/api/handlers/...`; streaming regressions live in bootstrap, forwarder, and protocol stream tests.
- WebSocket transcript allocation checks: `go test ./sdk/api/handlers/openai -run '^$' -bench BenchmarkNormalizeResponseSubsequentRequestTranscripts`.

## ANTI-PATTERNS
- Do not bypass ExecuteWithAuthManager/ExecuteStreamWithAuthManager from new protocol endpoints.
- Do not share mutable interceptor chunks/headers across calls or publish duplicate usage for nested execution.
- Do not overwrite CPA-reserved headers with upstream values or forward hop-by-hop/gateway telemetry headers.
- Do not assume interfaces containing typed nil plugin-host pointers compare equal to nil.
- Do not flush inside StreamForwardOptions callbacks or emit a second terminal payload after ChunkError.
- Do not classify HTTP 408 as invalid_request_error; preserve retryable request_timeout/server_error semantics.
- Do not stream Responses compact requests or expose internal WebSocket timing telemetry through SSE.
- Do not expand incremental WebSocket v2 input with previous_response_id into a full transcript.
- Do not retain caller-owned transcript slices after merging; output must own its bytes.
- Do not drop matching tool-call/output pairs; preserve the named standalone-call exceptions in the repair code.
- Do not rotate credentials within connection-scoped continuation; close and require full replay on a new socket.
- Do not add a second duplex reader or bypass its bounded input queue and ordered event shutdown.
- Do not rewrite disabled response.steer frames into response.create.
