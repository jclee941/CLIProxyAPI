# PLUGIN HOST KNOWLEDGE BASE

## OVERVIEW
Native plugin lifecycle, capability adapters, and scoped RPC bridges; score 14, a distinct ABI/concurrency domain.

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Load/reload/unload orchestration | `host.go` | `Host.ApplyConfig`, quiesce, rollback, load tokens |
| Immutable capability view | `snapshot.go` | Published plugin/capability records |
| Platform discovery | `platform.go` | Versioned files and platform-tier preference |
| Native loading | `loader_unix.go`, `loader_windows.go` | C ABI, not Go's standard plugin package |
| Unsupported builds | `loader_unsupported.go`, `support*.go` | Build tags and support header |
| JSON-RPC contract | `rpc_client.go`, `rpc_schema.go` | Native calls and wire payloads |
| Executor integration | `adapters_executors.go`, `executor_route.go` | Translation and direct routing |
| Interception | `adapters_interceptors.go` | Cloning, ordering, schema-gated payloads |
| Auth/model adaptation | `auth_provider.go`, `adapters.go` | OAuth, atomic tokens, model registration |
| Native callbacks | `host_callbacks*.go`, `callback_contexts.go` | Caller and instance-scoped contexts |
| HTTP operations | `http_bridge.go`, `http_operation_bridge.go` | Transports, cancellation, operation ownership |
| Streams | `stream_bridge.go`, `http_stream_bridge.go`, `model_stream_bridge.go` | Queue and lifetime ownership |
| Frontend route auth | `frontend_http.go`, `frontend_http_pattern.go` | Pinned route owner/generation |
| Management/resources | `management.go` | Reserved paths and plugin dispatch |
| Capability delegation | `scheduler.go`, `model_router.go`, `quota_provider.go` | Selection and quota operations |
| CLI flags | `command_line.go` | Flag discovery and execution |

## CONVENTIONS
- Lifecycle serialization uses the cancellable `applyMu` channel; `mu` protects state.
- Replacement quiesces the old instance and restores it if replacement registration fails.
- Guarded clients stop admitting calls before draining in-flight work.
- Callback operations are scoped by plugin ID and instance key, not operation ID alone.
- Interceptor inputs are cloned, including nested metadata with cycle handling.
- A plugin panic permanently fuses that plugin for the process lifetime.
- Frontend route authentication pins owner/generation before bounded body consumption.
- Windows loaders use shadow copies and keep Go DLL modules mapped after shutdown.
- Run `go test -race ./internal/pluginhost` for package lifecycle and bridge checks.

## ANTI-PATTERNS
- Do not hold `Host.mu` across plugin RPC, loader open, or lifecycle callbacks.
- Do not release a canceled load token before its client physically shuts down.
- Do not reopen calls to a fused or closed plugin client.
- Do not reuse foreign or closed callback contexts to open operations or streams.
- Do not run core authentication for a scoped frontend route.
- Do not read frontend bodies before route authentication succeeds.
- Do not mistake an unchanged single-frame translator fallback for a translated stream.
- Do not let request-scoped stop errors cause credential failover or cooldown changes.
- Do not hot-unload Windows Go DLLs with `FreeLibrary`.
