# GEMINI TRANSLATORS

## OVERVIEW
Gemini content conversion, Interactions bridges and Responses grounding/replay; score 14, a distinct 36-file provider domain.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Claude requests/responses | `claude/` | Tool results and thinking compatibility |
| Self-format normalization | `gemini/gemini_gemini_request.go` | Roles and missing response names |
| Safety defaults | `common/safety.go` | DefaultSafetySettings and attachment |
| Interactions routing | `interactions/init.go` | Both directions plus Interactions passthrough |
| Interactions shape mapping | `interactions/interactions_gemini_common.go` | Case conversion, config and step lifecycle |
| Chat requests | `openai/chat-completions/gemini_openai_request.go` | Tool-choice restrictions and media |
| Responses requests | `openai/responses/gemini_openai-responses_request.go` | Item ordering and function-output pairing |
| Responses streams | `openai/responses/gemini_openai-responses_response.go` | Reasoning, text, tool and search output items |
| Search and citations | `openai/responses/gemini_openai-responses_web_search.go` | Capability checks, grounding merge, rune offsets |
| Reasoning carriers | `openai/responses/signature_carrier.go` | Encoding, validation and spoof stripping |
| Late signatures | `openai/responses/trailing_signature.go` | Replay cache and ordering |
| Domain tests | `go test ./internal/translator/gemini/...` | Repository-root command |

## CONVENTIONS
- Initial instructions map to systemInstruction; later Chat system/developer turns use reminder envelopes.
- Responses search checks static capability vetoes as well as dynamic model capabilities.
- Grounding chunks may precede or follow supports; merging tracks cumulative raw indices and deduplicated chunks.
- Citation conversion maps upstream byte offsets into client-visible rune offsets across message parts.
- Responses carriers use `cpa-gemini-responses-carrier-v1:` with direction and semantic target.
- Text-signature replay cache keys use `gemini-responses-text:<messageID>`.
- `_cpa_reasoning_*` fields are transient pipeline metadata, never trusted client annotations.
- Canonical Gemini safety defaults cover five harm categories with BLOCK_NONE unless already configured.

## ANTI-PATTERNS
- Do not enable search when static native_capabilities.web_search is explicitly false.
- Non-web grounding or empty URIs must not synthesize a web_search_call.
- Do not parse stringified function-output JSON into objects; preserve the response result string.
- Orphan outputs become user text, not unpaired functionResponse parts.
- Never bind a later thought signature across intervening thought text to an earlier visible message.
- Cache failures must preserve signature order through fallback carriers, not drop replay state.
- Reject nested carrier envelopes and strip spoofed internal carrier fields before interpreting them.
- Chat tool-name collisions, undeclared selected tools and explicit parallel_tool_calls=false fail closed to NONE.
- A bare [DONE] on an unstarted Responses stream must not emit a successful completion.
