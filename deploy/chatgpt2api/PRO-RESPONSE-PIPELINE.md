# Pro Response Pipeline Candidate

`0004-pro-response-pipeline.patch` is a source-backed parser and terminal-state
correction, not a production rollout or a claim that the live Pro incident is
fully resolved. It preserves the vendor license and dependency stack.

## Base And Application

Use a clean, isolated checkout of `basketikun/chatgpt2api` at
`e55aef2829e7bf1d7256d6ff3feb4b40b02743d2`. Apply these files in order with
`git apply`, using absolute paths to this directory:

1. `0001-local-fixes-logs-effort-and-transient-retry.patch`
2. `0002-english-ui-and-locale.patch`
3. `0003-image-policy-errors-and-poll-diagnostics.patch`
4. `0004-pro-response-pipeline.patch`

The local base patch is required. This sequence implements the existing README's
"existing local patch", then `0002`, then `0003` recipe. SHA-256 comparison of
the five relevant deployed Python files against this pre-0004 base matched:
`conversation.py`, `openai_v1_chat_complete.py`, `chat_completion_cache.py`,
`openai_backend_api.py`, and `model_service.py`.

Run from the isolated vendor checkout:

```sh
uv sync --frozen
.venv/bin/python -B -m test.run_pro_pipeline_offline
.venv/bin/python -B -m test.run_image_poll_offline
uvx basedpyright --pythonpath .venv/bin/python --level error \
  services/protocol/text_stream.py services/protocol/conversation.py \
  test/test_pro_response_pipeline.py test/run_pro_pipeline_offline.py
```

The Pro runner copies only Python source into a disposable sandbox, uses a
non-secret offline auth sentinel, and blocks socket connects and curl-cffi HTTP.
It does not copy configuration, accounts, cache data, or credentials. Do not run
the vendor's live HTTP smoke-test modules as an offline suite.

## Verified Mechanisms And Confidence

The pinned vendor's `docs/upstream-sse-conversation.md` documents empty
`v.message` add frames, explicit and pathless text patches, JSON-string payloads,
batched patches, `message.status`, and `message.end_turn`.

- **Confirmed by offline RED/GREEN:** `apply_text_patch()` accepted pathless
  strings only when `current_text` was already nonempty. An empty assistant add,
  explicit empty append, then `{"v":"OK"}` lost the answer. JSON-string payloads
  were also classified as non-message events and ignored.
- **Confirmed by offline RED/GREEN:** visibility was checked on full messages,
  but subsequent patches were applied to one global text buffer without their
  assistant channel or recipient. Analysis/tool patches could leak, and message
  transitions did not reliably reset patch state. The new decoder reconstructs
  the current message before the existing visibility filter runs.
- **Confirmed by offline RED/GREEN:** `stream_text_deltas()` marked use and
  returned on exhaustion without checking a successful terminal assistant
  message. Both completion wrappers could manufacture empty content plus
  `finish_reason: stop`; the cache then stored that result. The correction raises
  before success/account-use/cache publication for missing or failed terminal
  answers. Both `status: finished_successfully` and explicit `end_turn: true`
  are required; a missing turn marker is not inferred as success. Existing cache
  exception cleanup and waiter notification remain in
  use; successful results remain cached. The cache is not disabled.

The decoder applies only to text conversations. Image parsing, image polling,
model selection, generation budgets, account policy, and network deadlines are
not changed. Ordinary refusal text remains text; internal tool messages remain
internal, including a failed internal tool followed by a valid final answer.
This backend does not provide general OpenAI function calling, and
this patch does not invent support or reinterpret a tool-only upstream turn as
a text answer. Existing web-search and image-tool routes are retained.

## Model And Async Findings

The authoritative model catalog comes from upstream `/backend-api/models`:
`OpenAIBackendAPI.list_models()` exposes returned slugs, `ModelCatalogService`
merges account-type catalogs, and account selection uses the requested model's
eligible types. `_conversation_payload()` passes the requested model unchanged.
No `gpt-6-pro` alias or name-based unsupported rule exists in this snapshot.

Text generation performs one conversation POST followed by SSE consumption.
There is no text-Pro asynchronous polling implementation or fixture. Image,
search, and editable-file polling are different contracts and are not evidence
for a Pro text polling protocol. No speculative poll or generation retry was
added. `max_tokens` participates in the cache key but is not applied as a text
generation budget by this vendor path.

**Production root-cause confidence remains limited:** these mechanisms are real
source defects, but the permitted downstream surface does not identify which
one caused the live empty response. An asynchronously unfinished Pro turn or an
additional upstream schema change is still possible. This patch reports such a
missing result honestly; it cannot recover an answer absent from the observed
stream without a verified upstream retrieval contract.

## Runtime Evidence And Rollout Gate

Exactly one fresh direct streamed diagnostic was sent to the existing sidecar,
with a unique harmless prompt and no automatic retry. The existing auth key was
derived inside the container process, never returned or passed as an argument.
Only sanitized response structure was observed:

```json
{"status":200,"delta_fields":["content","role"],"chunks":2,"text_length":0,"finish_flags":["stop"],"errors":0,"done":true}
```

This independently reproduces the empty-success symptom, without the earlier
non-stream cache-key ambiguity. It does not expose the upstream frame sequence,
reasoning, conversation state, or whether a delayed final answer exists.

An isolated real loopback HTTP server running the patched vendor route was also
driven with synthetic upstream frames. Valid non-stream and stream calls each
returned two content characters and exactly one success stop. Missing answers
returned HTTP 502 without choices. A partial stream returned HTTP 200 headers,
then `incomplete_upstream_response`, with no success stop. The server was stopped
after these checks. This is fixture-backed HTTP evidence, not live ChatGPT proof.

**Required request-scoped STOP policy before rollout:**
`error.code == "incomplete_upstream_response"` must terminate that request at CPA
and other callers, without automatic retry, account rotation, or resubmission.
Before any output this error uses HTTP 502, which a proxy may otherwise retry.
After output it is an SSE error under already-sent HTTP 200 headers. Neither is
an authentication failure or a reason to relogin. A generated answer might have
been lost; repeating generation may duplicate work or charges. This patch does
not modify CPA's global retry setting or implement the required CPA policy.

The coordinating operator must review that retry behavior and personally
validate the actual Pro final-answer path before considering rollout. No
production files, containers, accounts, or logging configuration were edited.
The permitted diagnostic may leave ordinary request/cache records; no cleanup
or deletion of production records was attempted.

## Validation And Limits

The new offline runner passed 59 tests, including real route handling, the real
cache, existing routing/cache/model tests, and the new frame regressions. The
image runner passed five scenarios and 42 tests. Applying only the final patch's
tests to the pre-0004 base produced 23 failing subtests/assertions and one missing
error-event assertion error across 59 tests; applying the source hunks made all
59 pass. The full patch passed clean apply and reverse-check against that base.
Syntax and focused basedpyright checks passed. The existing routing test's synthetic event fixture gained a
terminal event; its four pre-existing strict type errors are not suppressed or
refactored, and were reproduced on the pre-0004 base. LSP diagnostics on the fresh
replay checkout were clean for the changed production code and new test files.
No full vendor suite or live upstream success is claimed.
