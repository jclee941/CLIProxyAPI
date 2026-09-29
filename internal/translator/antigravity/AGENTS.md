# ANTIGRAVITY TRANSLATORS

## OVERVIEW
Antigravity request envelopes, response unwrapping and signature replay; score 14, a distinct 32-file provider domain.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Claude requests | `claude/antigravity_claude_request.go` | Thinking, tool pairing, name mapping |
| Claude replay validation | `claude/signature_validation.go` | Native signatures and directional carriers |
| Claude search | `claude/web_search.go` | Typed search tools and cited text |
| Gemini requests | `gemini/antigravity_gemini_request.go` | Signature sanitization and CLI tool-response grouping |
| Gemini completion | `gemini/antigravity_gemini_response.go` | Terminal synthesis and usage restoration |
| Interactions conversion | `interactions/` | Direct request mapping and step lifecycle |
| Chat completion | `openai/chat-completions/` | Deferred finish reason and tool names |
| Responses envelope | `openai/responses/init.go` | Additional SDK request-envelope registration |
| Dedicated search requests | `openai/responses/antigravity_openai-responses_request.go` | Capability isolation and search-only envelope |
| Regression coverage | Pair-local `*_test.go` | Signature, grounding and no-op cases |
| Domain tests | `go test ./internal/translator/antigravity/...` | Repository-root command |

## CONVENTIONS
- Requests carry `project`, `model` and nested `request`; generated payloads arrive beneath `response`.
- Responses conversion composes the Gemini Responses implementation with Antigravity-specific wrapping.
- Native Google Search belongs in dedicated Responses web-search envelopes; normal-chat fallback strips it.
- Claude-wire Gemini carriers encode direction and semantic target with `cpa-gemini-carrier-v1:`.
- Tool names are sanitized/disambiguated on ingress and restored from request context on egress.
- `cpaUsageMetadata` restoration preserves accounting after upstream filtering.
- Canonical no-op transformations have byte-slice reuse tests, not just JSON equality tests.
- Chat finish reasons are cached until token accounting is available; `[DONE]` supplies the fallback terminal path.

## ANTI-PATTERNS
- Never inject dummy Claude thinking blocks; Antigravity validates their signatures.
- An explicitly invalid or incompatible client signature must not be replaced from recovery cache.
- Do not replay Claude signatures on functionResponse parts or non-model parts.
- Do not normalize a functionResponse turn to the model role.
- Empty envelopes and keepalives must not make an otherwise empty stream look successful.
- Do not finalize Chat Completions solely because a usage-only chunk arrived.
- Do not mix native Google Search and function declarations in a normal chat envelope.
- No-copy parsing retains input-backed slices; keep the input immutable while those slices are used.
