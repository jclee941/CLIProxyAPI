# CODEX DESTINATION TRANSLATORS

## OVERVIEW
Codex Responses payload conversion and reverse stream adaptation; score 14, a distinct 32-file provider domain.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Claude input | `claude/codex_claude_request.go` | Schema normalization, call IDs, compat thinking |
| Claude output | `claude/codex_claude_response.go` | Deferred events and serialized content blocks |
| Claude search output | `claude/codex_claude_response_web_search.go` | Server tool and result blocks |
| Parallel tool regression | `claude/codex_claude_parallel_function_calls_test.go` | Block lifecycle assertions |
| Gemini input | `gemini/codex_gemini_request.go` | FIFO pairing and media conversion |
| Gemini output | `gemini/codex_gemini_response.go` | Image deduplication and incomplete terminals |
| Interactions | `interactions/` | Direct step traversal and Codex input items |
| Chat adaptation | `openai/chat-completions/` | Tool strictness, names and stream deltas |
| Responses normalization | `openai/responses/codex_openai-responses_request.go` | Unsupported fields and system roles |
| Domain tests | `go test ./internal/translator/codex/...` | Repository-root command |
| History benchmark | `go test -run='^$' -bench=. ./internal/translator/codex/claude` | Large Claude histories |

## CONVENTIONS
- Codex request conversion forces streaming even when the client-facing response is non-streaming.
- Native Responses inputs still need system-to-developer normalization and unsupported-field removal.
- Tool names and call IDs obey 64-character limits; shortening must retain stable reverse mappings.
- Missing Gemini call IDs use deterministic `call_gemini_` sequence IDs paired through a FIFO queue.
- Chat strict defaults differ from Responses defaults; forward explicit false when required.
- Claude reasoning summaries can span multiple parts of one Codex reasoning item.
- The final reasoning signature arrives on output_item.done, not summary-part completion.
- Tool arguments may precede function names; Claude streaming buffers them before block announcement.

## ANTI-PATTERNS
- Never emit a pre-content encrypted-content snapshot as the final thinking signature.
- Do not close a reasoning block merely because one summary part ended.
- Do not carry an unfinished reasoning item's open block into the next item.
- Do not emit an empty assistant message when the only content is function calls.
- Do not opt into reasoning summaries solely because a reasoning effort was requested.
- Do not mark schemas strict when declared optional properties make strict mode unsatisfiable.
- Strip unsupported schema dialect keywords and unsupported Unicode-property regex patterns.
- Do not forward token limits, user, prompt_cache_breakpoint or context_management on native Codex Responses requests.
