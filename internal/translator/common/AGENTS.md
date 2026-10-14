# SHARED TRANSLATOR HELPERS

## OVERVIEW
Protocol-specific primitives shared by converter pairs; score 15, with 38 Go files (18 non-test), imported by 85 translator files plus `internal/runtime/executor` helpers.

Converter architecture and registry contracts: [parent guide](../AGENTS.md).

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
| Unsendable parts | `parts.go` | UnsupportedPartError, UserTurnDrops, UserRun |
| Native Claude JSON as SSE | `claude_native_response.go` | ClaudeMessagesJSONToSSE |
| apply_patch request side | `apply_patch_responses.go` | NormalizeApplyPatchResponsesRequest, ApplyPatchResponsesBridge |
| apply_patch streaming | `apply_patch_input.go`, `apply_patch_events.go` | Argument decoder, delta/done/failure payloads |
| Helper tests | `go test ./internal/translator/common` | Repository-root command |

## CONVENTIONS
- JoinRawArray consumes already-serialized JSON items; an empty input produces `[]`.
- SetRawArrayItems targets an existing empty array; empty items leave the input unchanged.
- Its single-item path splices directly into the existing empty-array slot.
- SetStringWithoutHTMLEscape preserves literal `<`, `>` and `&` in JSON strings.
- Responses output reconciliation tries exact identity, function name, then FIFO matching.
- Gemini user-part ordering places text before functionResponse for Vertex compatibility.
- `MergeAdjacentGeminiContents` may combine user tool-result turns; `MergeAdjacentGeminiUserContents` preserves those boundaries.
- DeriveClaudeUserID prefers session identity before stable conversation-derived fallback.
- A dropped part is allowed while its user turn keeps one sendable part; the first emptied turn becomes the request error.
- apply_patch normalization collects declaration winners before rewriting, so precedence never shifts.
- Bridge events are JSON payloads without SSE framing; callers frame them.
- The bridge consumes Responses response paths in claude, codex, gemini, openai/openai and openai/interactions.

## ANTI-PATTERNS
- Do not merge consecutive Gemini model turns here; part indices participate in signature replay.
- Do not merge Gemini user turns containing functionResponse through the user-only merge helper.
- Never steal or rewrite an unmatched explicit Responses call ID to satisfy another pending call.
- Do not reinterpret tool-result JSON containing string-valued $ref as media references; preserve opaque text.
- Do not apply ordinary message cache-control placement to nested Claude tool-result content.
- Never copy an ApplyPatchInputDecoder after use or share an ApplyPatchResponsesBridge across goroutines or responses.
- Do not reveal a folded dispatcher name as the selected child tool name; identity checks withhold names.
- Do not expose upstream arguments in apply_patch failure events.
- Do not substitute broad JSON remarshal logic for byte-helper contracts without checking their no-op and escaping tests.
