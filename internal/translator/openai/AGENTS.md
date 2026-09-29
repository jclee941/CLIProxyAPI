# OPENAI TRANSLATORS

## OVERVIEW
Chat Completions destinations, Responses adaptation and bidirectional Interactions; score 14, a distinct 41-file domain.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Claude input | `claude/openai_claude_request.go` | Tool adjacency, reasoning and tool-result images |
| Claude output | `claude/openai_claude_response.go` | Sequential blocks, belated names and usage |
| Gemini input | `gemini/openai_gemini_request.go` | Deterministic IDs and FIFO response matching |
| Gemini output | `gemini/openai_gemini_response.go` | Partial argument parsing and multi-choice parts |
| Chat self-format | `openai/chat-completions/` | Model rewrite, passthrough, done marker |
| Responses input | `openai/responses/openai_openai-responses_request.go` | Instructions, reasoning, media and output pairing |
| Responses output | `openai/responses/openai_openai-responses_response.go` | Synthesized item lifecycle and incomplete status |
| Responses tool identity | `openai/responses/openai_openai-responses_tools.go` | Namespaces and collision resolution |
| Request-scoped lookup | `openai/responses/responses_tool_index.go` | Reverse identity and custom-tool recovery |
| Interactions chat routes | `interactions/chat-completions/` | Separate files per request direction |
| Interactions Responses routes | `interactions/responses/` | Both directions share request/response files |
| Domain tests | `go test ./internal/translator/openai/...` | Repository-root command |

## CONVENTIONS
- Images inside Claude tool results become a following synthetic user message, with placeholders in tool text.
- Unsigned assistant-thinking preservation is explicit in compat conversion variants.
- Gemini calls without IDs derive deterministic hashes from turn, part, name and arguments.
- Claude output accounting subtracts cached and cache-write tokens from OpenAI prompt tokens.
- Responses tool declarations are indexed once per request and restored to namespace/custom identities on output.
- Long names preserve the local tool-name tail under the 64-character cap; collisions receive distinct aliases.
- Interactions Responses conversion has separate Antigravity and Devin model-specific branches.
- Chat self-format requests reuse matching-model bytes and remember [DONE] across stream calls.

## ANTI-PATTERNS
- Never expose Claude redacted_thinking as reasoning_content.
- Only assistant tool_use blocks become Chat tool_calls; unknown tool_choice values fail closed.
- Do not separate tool messages from the preceding assistant's matching tool_calls.
- Do not discard nameless streamed tool calls; the Claude adapter has a belated-name fallback.
- Orphan Responses outputs become user text, not unpaired Chat tool messages.
- Partial or invalid argument JSON must not fabricate a successfully completed Responses tool item.
- Ambiguous local tool names must never dispatch to an arbitrary namespace.
- Deduplication and discarded custom declarations must not change surviving ordinary tools' identities.
- Preserve malformed video parts for upstream validation rather than silently degrading to text-only input.
