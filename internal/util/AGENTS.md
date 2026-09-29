# SHARED PAYLOAD UTILITIES

## OVERVIEW
Schema adaptation, tool identity, zero-copy JSON, and request metadata helpers; score 14, a central cross-provider utility domain.

## WHERE TO LOOK
| Task | File |
|------|------|
| Gemini/Antigravity schema cleaning | `gemini_schema.go` |
| Claude schema normalization | `claude_schema.go` |
| No-copy JSON readers | `gjson.go` |
| Repository buffer-write guard | `nocopy_invariant_test.go` |
| Bidirectional tool-name mapping | `translator.go` |
| Responses namespace/custom tools | `responses_tools.go` |
| Claude tool IDs and Gemini provenance IDs | `claude_tool_id.go` |
| Claude result content conversion | `claude_tool_result.go` |
| Billing attribution detection | `claude_attribution.go` |
| Header templates/session context | `header_helpers.go` |
| Provider resolution and redaction | `provider.go` |
| Config-aware proxy selection | `proxy.go` |

## CONVENTIONS
- No-copy GJSON results alias the caller's bytes; payload ownership is part of the API contract.
- Default allocating SJSON writes preserve already-derived string views.
- Tool sanitization maintains forward/reverse identity maps and deterministic collision handling.
- Responses tool descriptors support direct, namespace, and additional-tool representations.
- Header attributes can resolve `$CPA_SESSION_ID` and `$CLIENT_HEADER:Name` at request time.
- Gemini tool-use IDs encode specific synthetic provenance rather than arbitrary client identity.

## ANTI-PATTERNS
- Pass schema cleaners one schema, never a whole request/history document.
- Do not mutate the byte buffer while a no-copy JSON result is live or retain that result beyond ownership.
- Do not enable in-place SJSON writes without satisfying the repository invariant review/allowlist.
- Do not rewrite property names as schema keywords merely because they equal `title`, `format`, or `default`.
- Do not change numeric/boolean argument types merely to stringify private-backend enum members.
- Do not treat ordinary client tool IDs as CPA-generated provenance IDs.

Run `go test ./internal/util`; invariant checks scan source beyond this package.
Schema regressions belong in `gemini_schema_test.go`; tool identity regressions belong beside their mapping helpers.
