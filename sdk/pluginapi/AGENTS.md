# PLUGIN CAPABILITY CONTRACTS

## OVERVIEW
Plugin metadata, capability interfaces, and host/plugin payload schemas; score 11, distinct public extension contract.

## WHERE TO LOOK
| Task | Location | Detail |
|------|----------|--------|
| Registration metadata/capabilities | `types.go`: Plugin, Metadata, Capabilities | Feature advertisement and negotiated schema version. |
| Auth/model/scheduler integration | `types.go`: AuthProvider, ModelProvider, Scheduler, ModelRouter | Parse/login/refresh, discovery, selection, routing. |
| Provider execution and host callbacks | `types.go`: ProviderExecutor, HostHTTPClient, HostModel* | Payloads, execution routing, stream handles. |
| Translation and interception | `types.go`: *Translator, *Normalizer, *Interceptor | Protocol conversion versus request/response hooks. |
| Request completion/observation | `types.go`: RequestLifecyclePlugin, WebSocketResponseObserver | Lifecycle outcomes and upstream response events. |
| Management, CLI, usage, quota | `types.go` | Separate extension interfaces and result models. |
| Authenticated frontend routes | `frontend_http.go` | Route registration, route-local auth, buffered handler I/O. |
| Contract regression checks | `types_test.go`, `frontend_http_test.go` | Serialization and interface coverage. |

## CONVENTIONS
- Capability flags advertise optional interfaces; keep advertised behavior aligned with host adapters.
- ModelRouteResponse distinguishes self, executor, and provider targets.
- Host model execution carries entry/exit protocol, optional pinned auth, and proxy override explicitly.
- StreamChunkHeaderInitIndex is the header-init sentinel, separate from payload indices >= 0.
- Request bodies and history are fresh header-init snapshots; payload omission follows negotiated pluginabi gates.
- ManagementResponse carries raw body bytes; representation compatibility belongs to the native adapter.
- Quota result types have custom UnmarshalJSON paths; inspect those before changing field shapes.
- Frontend HTTP is buffered, not streaming: bodies capped at 128 MiB and RPC messages at 192 MiB.
- FrontendHTTPAuthCore is the empty/default mode; scoped routes supply their own Authenticator.
- Scoped authentication runs before body reading and therefore receives nil Body.
- Native scoped routes reuse the owner's frontend_auth.authenticate adapter without global frontend-auth registration.
- Standard host routes win collisions; overlapping plugin routes use plugin priority.
- Frontend route paths permit literal segments, {name}, and {name}:action after a literal first segment.
- CallerScope is the host-derived namespace restored by scoped auth, not a newly invented plugin identity.
- Plugins scope stored resources and upload sessions to CallerScope.
- Frontend request Scheme reflects inbound transport; response bytes bypass JSON/HTML body rewriting.
- Focused checks: `go test ./sdk/pluginapi`.

## ANTI-PATTERNS
- Do not fall back to core authentication after failed route-local scoped authentication.
- Do not read a request body inside scoped authentication or derive CallerScope from raw request fields.
- Do not register wildcard, encoded, traversal, or empty-segment frontend paths.
- Do not confuse frontend HTTP routes with management/resource routes or expose internal handler interfaces over JSON.
- Do not reintroduce full request/history payloads on every stream chunk without respecting negotiated compatibility.
