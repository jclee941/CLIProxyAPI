# NATIVE PLUGIN WIRE ABI

## OVERVIEW
Stable native-plugin RPC names, JSON envelopes, and version gates; score 11, distinct cross-process compatibility boundary.

## WHERE TO LOOK
| Task | Location | Detail |
|------|----------|--------|
| Native export compatibility | `types.go`: ABIVersion | C ABI shape, currently 1. |
| Registration negotiation | `types.go`: SchemaVersion | JSON contract, currently 6. |
| RPC method dispatch identifiers | `types.go`: Method* constants | Plugin, auth, executor, translation, quota, host callbacks. |
| Serialize success/failure | `types.go`: Envelope | OK, raw Result JSON, optional Error. |
| Surface plugin errors | `types.go`: Error/NewError/NewErrorEnvelope | Code, message, retryability, optional HTTP status. |
| Check wire stability | `types_test.go` | Round trips, method values, scheduler name, error constructors. |

## CONVENTIONS
- ABIVersion and SchemaVersion track different compatibility dimensions.
- Schema version is negotiated at plugin.register, not inferred from method presence.
- v2 adds request lifecycle completion and active request termination.
- v3 omits request bodies on payload stream chunks; header-init retains them.
- v4 introduces upstream WebSocket response observation.
- v5 omits history chunks on payload stream chunks; header-init retains history.
- v6 preserves raw management-response JSON without HTML escaping.
- Named SchemaVersion* thresholds let adapters retain older plugin behavior.
- Method values are wire identifiers, including underscore/dot distinctions.
- Envelope.Result is json.RawMessage, not an already-quoted JSON string.
- Error.StatusCode returns the embedded value, including zero when unset.
- The host interprets omitted/zero HTTPStatus as its default internal-server-error response.
- Error.Error and StatusCode are nil-safe.
- NewError accepts an optional HTTP status; NewErrorEnvelope marshals the failed envelope.
- Focused check: `go test ./sdk/pluginabi`.

## ANTI-PATTERNS
- Do not rename RPC values as cosmetic Go identifier cleanup.
- Do not bump C ABIVersion merely for a JSON schema change.
- Do not remove older-schema behavior when extending host/plugin adapters.
- Do not double-encode raw Result JSON inside an envelope.
- Do not assume Error.StatusCode itself substitutes HTTP 500 for zero.
