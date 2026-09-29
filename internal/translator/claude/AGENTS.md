# CLAUDE DESTINATION TRANSLATORS

## OVERVIEW
Claude Messages requests and reverse protocol responses; score 14, a distinct 30-file domain with extensive Responses state machines.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Gemini input | `gemini/claude_gemini_request.go` | FIFO tool IDs and MIME-sensitive media |
| Gemini output | `gemini/claude_gemini_response.go` | SSE accumulation and ordered parts |
| Interactions bridge | `interactions/` | Role grouping, step events, error results |
| Chat input | `openai/chat-completions/claude_openai_request.go` | Cache controls, instructions, tool choice |
| Chat output | `openai/chat-completions/claude_openai_response.go` | Cached usage and trailing usage chunk |
| Responses input | `openai/responses/claude_openai-responses_request.go` | Tool pairing repair and model-specific prefill stripping |
| Responses output | `openai/responses/claude_openai-responses_response.go` | Output indices, reasoning and terminal status |
| Server search | `openai/responses/claude_openai-responses_web_search.go` | Search IDs and encrypted replay |
| Ordering regressions | `openai/responses/*reasoning_order*`, `*interleaved_search*` | Interleaved reasoning, tools and search |
| Domain tests | `go test ./internal/translator/claude/...` | Repository-root command |

## CONVENTIONS
- Shared Claude message accumulation groups adjacent roles while retaining cache boundaries.
- Shared user-ID derivation supplies `metadata.user_id` consistently across source formats.
- Several non-stream converters reconstruct final payloads from Claude SSE, not only single JSON objects.
- Responses instructions, system items and developer items become separate ordered top-level system blocks.
- The executor decides final system-block placement; Responses translation preserves operator authority.
- Tool-result cache controls are hoisted onto the enclosing tool_result block.
- Responses custom tools use namespace-qualified identities; direct names win over namespace aliases.
- `claude-redacted-thinking:` tunnels redacted data in Responses encrypted_content; it is not a provider signature.
- Search replay uses sanitized `srvtoolu_` IDs and genuine encrypted content.

## ANTI-PATTERNS
- Do not merge, trim or demote Responses system inputs to ordinary user text.
- Do not emit orphan tool_result blocks; pairing repair degrades unpaired outputs to text.
- Do not leave tool results behind other content in the immediately following user turn.
- Do not place cache_control inside tool_result.content child parts.
- Do not convert non-image inline media into Claude image blocks.
- Never expose redacted_thinking as Chat Completions reasoning_content.
- Do not fabricate encrypted search results; unreplayable entries and citations are dropped.
- Keep thinking format conversion separate from model-capability validation by ApplyThinking.
