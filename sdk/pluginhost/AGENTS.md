# PUBLIC PLUGIN HOST

## OVERVIEW
Embedding wrapper and public configuration/model conversion; score 9, distinct boundary with 30 LSP references to Host.

Parent map: [SDK guide](../AGENTS.md).

## WHERE TO LOOK
| Task | Location | Detail |
|------|----------|--------|
| Construct/configure the host | `host.go` | New, RuntimeConfig, ApplyConfig. |
| Preserve plugin configuration | `host.go` | PluginInstanceConfig.Raw and deepCopyYAMLNode. |
| Convert model projections | `host.go` | registryModelsToPluginModels and thinkingSupportToPlugin. |
| Parse or refresh credentials | `host.go` | ParseAuth, ParseAuths, RefreshAuth, AuthDataToCoreAuth. |
| Run plugin login | `host.go` | StartLogin/PollLogin and optional metadata. |
| Select through schedulers | `host.go` | PickAuth and SchedulerWantsAcrossPriorities. |
| Unload or stop plugins | `host.go` | Context-aware and background-context variants. |
| Discover/fetch/reset quotas | `quota.go` | Provider and plugin-specific operations. |
| Check public wrapper contracts | `host_test.go`, `quota_test.go` | Public construction and quota integration. |

## CONVENTIONS
- Construct with New; the zero Host has no inner runtime.
- Nil/uninitialized hosts return unhandled or empty results rather than creating a runtime.
- ApplyConfig normalizes plugin configuration and sanitizes OAuth model aliases.
- Runtime conversion copies alias slices, exclusion maps, and YAML Content trees.
- Public model conversion skips nil registry entries and copies modality/parameter/thinking-level slices.
- ModelsForAuth preserves the returned auth record, handled flag, and error alongside converted models.
- Context-aware unload/shutdown bounds waiting for active calls by the caller's context.
- Without a supplied context, unload/shutdown uses context.Background.
- Quota APIs retain `(response, handled, error)` so unsupported providers remain distinguishable.

## ANTI-PATTERNS
- Do not mistake PluginBusy for an in-flight call counter; it reports library loading/loaded state.
- Do not drop unknown plugin YAML fields while converting RuntimeConfig.
- Do not share mutable model slices with internal catalog projections.
- Do not infer quota support solely from provider naming; query the provider or owning plugin.

Focused check: `go test ./sdk/pluginhost`.
