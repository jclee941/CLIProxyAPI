# HOME CLIENT KNOWLEDGE BASE

## OVERVIEW
Home control-plane transport, dispatch fencing, and cumulative release delivery; score 11, a distinct protocol domain.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Transport and lifecycle | `client.go` | Redis clients, discovery, takeover, subscriptions |
| Dispatch ambiguity | `client.go` | `DispatchError`, `AbortAmbiguousDispatch`, lifetime fencing |
| Enrollment | `certificate.go` | JWT claims, CSR, mTLS material over RESP |
| Release acknowledgments | `concurrency_release.go` | Cumulative sequences and completion tickets |
| Current-client bridge | `global.go` | Atomic load/store and conditional clearing |
| KV semantics | `kv_helpers.go` | Required versus best-effort operations |
| Wire DTOs | `requests.go` | Dispatch and in-flight snapshot payloads |
| Plugin status delivery | `plugin_status.go` | Node-scoped report queue |
| Protocol fixtures | `testdata/*.json` | Dispatch, release, and in-flight contracts |
| Transport fault coverage | `client_test.go` | Connection loss, cancellation, failover |

## CONVENTIONS
- RESP commands carry Home-specific JSON contracts; this is not a generic Redis client.
- A post-send transport failure can be ambiguous, not safely retryable on the same lifetime.
- Abort fences dispatch and detaches connections; a new lifetime reprobes deployment capabilities.
- Current-client removal uses compare-and-swap when ownership matters.
- KV Required helpers return errors and Home-mode state; BestEffort helpers intentionally degrade.
- Release frames carry cumulative sequence numbers per credential/model group.
- A release ticket completes only when its sequence is acknowledged, not when locally queued.
- Prefer `RunConfigSubscriberLifetime` where subscriber errors must reach lifecycle management.
- Preserve root guidance's CLIProxyAPIHome sync rule for wire changes.
- Run `go test -race ./internal/home` for transport and release-delivery checks.

## ANTI-PATTERNS
- Do not carry a cached CAS-unsupported verdict into a fresh Home lifetime.
- Do not raise legacy dispatch Count above one when pinning or excluding credentials.
- Do not overwrite the permanent cancellation deadline in plugin-sync connections.
- Do not treat an ambiguous dispatch as evidence that Home never accepted it.
- Do not acknowledge cumulative release work before the sender confirms delivery.
- Do not configure only half of the client certificate/key pair.
- Do not accept negative KV TTLs or simultaneous NX and XX options.
