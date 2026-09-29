# THINKING AND SUMMARY INTENT

## OVERVIEW
Reasoning configuration, summary visibility, and provider application; score 14, a central cross-protocol semantic domain.

## WHERE TO LOOK
| Task | Location |
|------|----------|
| Engine entry points and plugin registration | `apply.go` |
| Model suffix parsing | `suffix.go` |
| Shared modes/levels/applier contract | `types.go` |
| Capability validation and clamping | `validate.go` |
| Level/budget conversion | `convert.go` |
| Summary visibility translation | `summary.go` |
| Configuration-update routing | `configuration_update.go` |
| Remove stale provider fields | `strip.go` |
| Provider output encoding | `provider/*/apply.go` |
| Typed HTTP-facing errors | `errors.go` |

## CONVENTIONS
- Retain the root thinking-pipeline architecture; source intent and target payload are distinct inputs.
- Supplied model metadata lets configured API-key capabilities override generic catalog assumptions.
- Summary visibility is represented independently from reasoning effort.
- Cross-family intent conversion differs from strict same-family validation.
- Provider appliers register through `init()`; executor helper blank imports activate builtins.
- Plugin appliers have owner/priority registration and explicit owner cleanup.
- Kimi aliases share one registered provider applier.
- Provider `Apply` methods are idempotent and return modified payload copies.

## ANTI-PATTERNS
- Do not mutate the input thinking config or model metadata in an applier.
- Do not invoke provider output logic with unvalidated configuration.
- Do not invent OpenAI chat effort from summary visibility alone.
- Do not emit Claude `thinking.display` without an active thinking mode.
- Do not output Gemini/Antigravity level and budget fields simultaneously.
- Do not rebuild a malformed target payload from a separate source configuration update.
- Do not override explicit native Kimi thinking with a legacy compatibility field.

Run `go test ./internal/thinking/...` and relevant `./test` thinking/summary integration cases.
