# TRANSLATION DISPATCH

## OVERVIEW
Format registry, plugin translation hooks, and envelope middleware; score 11, distinct protocol-dispatch domain.

## WHERE TO LOOK
| Task | Location | Detail |
|------|----------|--------|
| Format identity/constants | `format.go`, `formats.go` | Format, FromString, supported format names. |
| Register/dispatch transforms | `registry.go` | Native routes, fallback model rewrite, plugin hook precedence. |
| Carry request metadata | `pipeline.go`, `types.go` | RequestEnvelope and RequestEnvelopeTransform. |
| Add envelope middleware | `pipeline.go` | Request/response onion composition. |
| Define plugin hook surface | `plugin_hooks.go` | Normalize/translate request and response contracts. |
| Use default-registry helpers | `helpers.go`, `registry.go` | Package-level forwarding to Default(). |
| Activate built-in formats | `builtin/builtin.go` | Blank import populates the shared default registry. |
| Verify dispatch/fallback | `registry_test.go`, `registry_bytes_test.go`, `registry_summary_test.go` | Hook order, byte payloads, thinking summaries. |

## CONVENTIONS
- NewRegistry starts empty; Default is shared process-wide state.
- builtin.Registry returns the populated Default; builtin.Pipeline wraps that same registry.
- Request lookup is requests[from][to]; response lookup reverses registration direction as responses[to][from].
- Register wraps byte-only request transforms into envelope transforms without dropping the surrounding envelope.
- RegisterRequestEnvelope preserves request-scoped ModelInfo and configuration-update tracking.
- Native request transforms take precedence; plugin normalization owns the resulting provider payload.
- Without a native request transform, normalize the source before trying plugin translation.
- Fallback still rewrites model to the resolved name even when no translator handles the pair.
- With no hooks and no route, preserve source payload shape rather than injecting target-protocol summary fields.
- Track plugin edits to Responses configuration_update items separately from native translation changes.
- Response normalization runs before translation and after each resulting output chunk.
- Native stream transforms may intentionally return nil outputs; that is not a missing-route fallback.
- Pipeline middleware executes in registration order around its terminal registry call.
- NewPipeline(nil) selects Default; tests needing isolation can supply NewRegistry explicitly.
- Unregister removes both request and response entries and prunes empty pair maps.
- Focused checks: `go test ./sdk/translator ./sdk/translator/builtin`.

## ANTI-PATTERNS
- Do not treat HasResponseTransformer as proof that both stream and non-stream transforms exist; use specific probes.
- Do not replace intentional nil native stream output with the raw input chunk.
- Do not reverse request lookup to match response lookup; the asymmetry is intentional.
- Do not overwrite a plugin normalizer's removal of summary fields after native request translation.
- Do not mutate Default in tests without restoring registrations/hooks.
- Implementation changes under internal/translator remain governed by the root translator change policy.
