# TRANSLATOR KNOWLEDGE BASE

## OVERVIEW
Protocol conversion and registration hub; score 16, with eight immediate domains, 26 blank-imported pair packages and 266 Go files below this directory.

Parent package map: [internal/AGENTS.md](../AGENTS.md).

## STRUCTURE
```text
translator/
|-- init.go          # Side-effect imports activate converter packages
|-- translator/      # Thin wrapper around the SDK default registry
|-- common/          # Shared JSON, SSE, message and tool helpers
|-- antigravity/     # Antigravity envelope destinations
|-- claude/          # Claude Messages destinations
|-- codex/           # Codex Responses destinations
|-- gemini/          # Gemini destinations and Interactions bridge
|-- interactions/   # Claude-to-Interactions route and import guard
`-- openai/          # Chat destinations and bidirectional Interactions routes
```

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Activate a converter | `init.go` | Blank imports trigger pair registration |
| Registry facade | `translator/translator.go` | Register, Request, NeedConvert, Response, ResponseNonStream |
| Identify direction | Pair package `init.go` | Registration arguments are authoritative |
| Shared transformations | `common/` | Separate guidance for helper contracts |
| Claude to Interactions | `interactions/claude/` | Request, response and compat conversion |
| Interactions architecture | `interactions/import_boundary_test.go` | Scans four non-Gemini route trees for Gemini translator imports at any module major version |
| Context-aware requests | `claude/openai/responses/init.go`, `antigravity/openai/responses/init.go` | `RegisterRequestEnvelope`; plain request slot is nil |
| Unsendable parts | `common/parts.go` | Shared drop/refusal policy for request converters |
| Large payload costs | `request_benchmark_test.go`, `response_benchmark_test.go` | Root-package benchmarks |
| Scoped validation | `go test ./internal/translator/...` | Run from repository root |
| Payload benchmarks | `go test -run='^$' -bench=. ./internal/translator` | Requests and responses |

## CONVENTIONS
- Most paths mean destination/source, not request direction read left-to-right.
- Exceptions include bidirectional Interactions packages; inspect registration rather than guessing.
- Each registration pairs a forward request converter with reverse response hooks.
- Request converters return `([]byte, error)` (`sdk/translator/types.go` RequestTransform); the facade `Request` returns bytes only.
- Envelope converters receive request context, e.g. the pinned `upstream.claude.tool-search` value.
- The facade shares `sdk/translator.Default()`; runtime dispatch need not call this wrapper.
- Streaming hooks retain per-stream state through `param *any`; non-stream hooks are separate.
- Converters predominantly query raw JSON with gjson and build payloads with sjson.
- `originalRequestRawJSON` and translated `requestRawJSON` serve different identity-recovery roles.
- `chat-completions` directories declare `package chat_completions`; hyphenated Responses filenames are intentional.
- `interactions/claude` uses `function_call.id` but `function_result.call_id` on the Interactions wire.

## ANTI-PATTERNS
- Apply [the root translator change policy](../../AGENTS.md) before modifying this subtree.
- Do not route non-Gemini Interactions conversions through Gemini translators; `interactions/import_boundary_test.go` rejects those imports.
- Do not silently drop parts that empty a user turn; record them with `common.UserTurnDrops` so the request is refused.
- Do not assume equal source/destination formats mean no work; self-format converters normalize requests and terminal events.
- Do not treat registration reference counts as the full runtime call graph; dispatch is indirect.
