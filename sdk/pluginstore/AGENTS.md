# PUBLIC PLUGIN ARTIFACT CLIENT

## OVERVIEW
Registry/release clients and installation facade; score 9, distinct embedding contract with 23 LSP references to Client.

Parent map: [SDK guide](../AGENTS.md).

## WHERE TO LOOK
| Task | Location | Detail |
|------|----------|--------|
| Construct a registry client | `pluginstore.go` | NewClient and injected HTTPDoer. |
| Supply authentication | `pluginstore.go` | NewClientWithAuth and resolved-auth constructors. |
| Expire resolved credentials | `pluginstore.go` | NewClientWithResolvedAuthExpiry, ClearAuth. |
| Partition shared API cooldowns | `pluginstore.go` | WithNetworkScope. |
| Fetch registry/releases | `pluginstore.go` | FetchRegistry, FetchLatestRelease, FetchReleaseByTag. |
| Install a chosen artifact | `pluginstore.go` | Install, InstallVersion, InstallManifest. |
| Project release metadata | `pluginstore.go` | ManifestFromRelease, ManifestFromPlugin, SelectArtifact. |
| Verify public forwarding | `pluginstore_test.go` | Registry/install facade behavior. |
| Verify network partitioning | `network_scope_test.go` | Shared egress cooldown scopes. |

## CONVENTIONS
- Constructors trim the registry URL and retain the injected HTTP client.
- NewClientWithAuth normalizes auth configuration before constructing the internal client.
- Resolved auth may carry an expiry; the non-expiry constructor supplies the zero time.
- WithNetworkScope returns a Client copy with a trimmed scope.
- Clients sharing egress and credentials should use the same scope; empty scope denotes direct egress.
- Network scope labels cooldown identity only; configure HTTP transport separately.
- ClearAuth clears resolved auth and expiry and tolerates a nil receiver.
- InstallVersion selects by release tag/version; InstallManifest accepts an already projected manifest.
- ErrLoadedPluginLocked is the public alias for loaded-artifact replacement failures.

## ANTI-PATTERNS
- Do not discard the Client returned by WithNetworkScope.
- Do not assume ClearAuth removes configured unresolved auth rules or replaces the HTTP client.
- Do not interpret successful metadata selection as installation; invoke the chosen install operation.
- Do not substitute a network-scope string for actual proxy configuration.

Focused check: `go test ./sdk/pluginstore`.
