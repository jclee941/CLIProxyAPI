# SHARED TRANSLATOR HELPERS

## OVERVIEW
Protocol-specific primitives shared by converter pairs; score 12, with 25 files and a high-reference JSON/SSE helper surface.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Raw JSON arrays and SSE | `bytes.go` | JoinRawArray, SetRawArrayItems, event framing |
| Cache boundaries | `cache_control.go` | Message, part and tool-result controls |
| Claude turn assembly | `claude_messages.go` | Accumulator and tool-result alignment |
| Claude instruction wrappers | `claude_system.go` | Reminders and structured output instructions |
| Stable Claude user IDs | `claude_user_id.go` | Session keys and first-turn fallback |
| Gemini turn ordering | `gemini.go` | Thought parts, merging and function responses |
| Chat tool adjacency | `openai_tools.go` | AlignOpenAIToolCallMessages |
| Responses call matching | `responses.go` | Identity extraction and output reconciliation |
| Antigravity tool provenance | `antigravity_tools.go` | ExternalToolPrefix round trip |
| Devin-specific tools | `devin_tools.go` | Automation tool filtering and descriptions |
| File data normalization | `file_data.go` | Raw base64 versus data URL inputs |
| IDs and model selection | `request.go` | Claude call IDs and RequestModelName |
| Interactions accounting | `interactions_usage.go` | Common usage object |
| Helper tests | `go test ./internal/translator/common` | Repository-root command |

## CONVENTIONS
- JoinRawArray consumes already-serialized JSON items; an empty input produces `[]`.
- SetRawArrayItems targets an existing empty array; empty items leave the input unchanged.
- Its single-item path splices directly into the existing empty-array slot.
- SetStringWithoutHTMLEscape preserves literal `<`, `>` and `&` in JSON strings.
- Responses output reconciliation tries exact identity, function name, then FIFO matching.
- Gemini user-part ordering places text before functionResponse for Vertex compatibility.
- DeriveClaudeUserID prefers session identity before stable conversation-derived fallback.

## ANTI-PATTERNS
- Do not merge consecutive Gemini model turns here; part indices participate in signature replay.
- Do not merge Gemini user turns containing functionResponse through the user-only merge helper.
- Never steal or rewrite an unmatched explicit Responses call ID to satisfy another pending call.
- Do not reinterpret tool-result JSON containing string-valued $ref as media references; preserve opaque text.
- Do not apply ordinary message cache-control placement to nested Claude tool-result content.
- Do not substitute broad JSON remarshal logic for byte-helper contracts without checking their no-op and escaping tests.
