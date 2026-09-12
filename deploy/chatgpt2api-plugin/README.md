# ChatGPT2API Native Integration Plugin

`chatgpt2api` version `1.0.1` is a routing/management plugin that **delegates
inference to the existing native provider engine**. It is not a rewritten
ChatGPT backend. It registers only `model_router` and `management_api`.
There is no executor, model registrar/provider, auth provider, account migration,
credential lookup, host callback, or core patch in this module.

The existing host AuthManager, provider key, account pool, OpenAI-compatible
executor, aliases, model catalog, and ChatGPT2API sidecar remain authoritative.
The plugin does not add models or inspect/forward inference payloads.

## Build And Verify

```sh
make check
make smoke
```

Requires Go 1.26+, a C compiler, and Linux libc. The delivered artifact is
linux/amd64, Go 1.26.5, glibc; rebuild for a different runtime/libc. Output is
`chatgpt2api.so` and the generated `chatgpt2api.h`. There is no SDK Go ABI import.
The only external Go dependency is YAML v3, used for the host config payload.
`build-manifest.json` is build evidence, not a plugin-store manifest.

Tests first failed for missing routing/management implementation and missing
C ABI fixture/artifact, then passed after implementation. The checks include:

- Race-enabled routing/reload tests, exact-case matching, provider availability,
  empty allowlists, shared-model rejection, untouched model aliases and payloads.
- Loopback HTTP health fixtures: GET only, no caller headers/body, no redirects,
  malformed/oversized/down responses, safe errors, optional aggregate counts,
  and omission of unknown upstream fields and synthetic secret sentinels.
- Actual production `.so` loading in `smoke/c/load.c`: ABI validation, registration,
  provider routing/decline, resource bytes/MIME, method errors, unknown methods,
  invalid pointers/lengths, free, repeated shutdown, and zero host callbacks.
- `abismoke`-tagged C-entrypoint round trips for healthy status and concurrent
  route-counter/free/shutdown behavior against a loopback HTTP fixture. This
  instrumentation is excluded from the production `.so`; its endpoint override
  exists only in test code. No live health or inference requests are made by tests.
- `go vet` with and without the fixture build tag.

## Configuration

The host installs the library with plugin ID `chatgpt2api` and owns enablement.
Under the host's plugin config for that ID:

```yaml
model_names: []
dashboard_path: /CLIProxyAPI/plugins/chatgpt2api/index.html
```

`model_names` is required explicitly; an empty list declines every route.
Populate it only with the integrating operator's runtime-derived exact model
IDs. There is deliberately no default catalog or hardcoded list of 25 models.
Trimmed blank entries, duplicates, non-string entries, and shared `auto`,
`gpt-image-2`, or `universalauto` entries are rejected. Reserved-name rejection
is case-insensitive; normal model matching is exact and case-sensitive after
trimming. Other cross-provider collisions must be excluded by the operator.

The plugin routes only if the requested model is in that list and normalized
`AvailableProviders` contains `openai-compatible-chatgpt2api`, the core's internal
provider key. `chatgpt2api` remains the public integration/configuration name.
Otherwise it declines without
changing provider, body, model, aliases, or fallback behavior. It does not use
`SourceFormat`, `Body`, headers, query, or host auth/config APIs to decide.

Successful config reload replaces the allowlist atomically; invalid reload
retains the previous configuration. The aggregate `route_count` is retained
across registration/reconfiguration and starts at zero in a new process.
It counts **selected routing attempts, not successful inference**. A later
host auth error or upstream failure still counts as a selected routing attempt.

`dashboard_path` defaults as shown. It must be an absolute `.html` path and is
trusted operator configuration, never a client query/path. Mount the separately
built `web/index.html` there as a static, non-secret file. Resources are returned
verbatim without server-side state injection. The backend does not build or
modify `web/` or `DESIGN.md`.

## Management Contract

`GET /v0/management/plugins/chatgpt2api/status` uses **host management
authentication**, not a second key or a plugin-owned authentication scheme:

```json
{
  "plugin": {"id": "chatgpt2api", "version": "1.0.1", "route_count": 0},
  "routing": {
    "provider": "chatgpt2api",
    "mode": "native-provider",
    "model_names": [],
    "credential_source": "host-auth-manager"
  },
  "upstream": {
    "healthy": false,
    "error": "health_unavailable"
  }
}
```

On a valid health response, `upstream` may additionally include `version` and
`accounts` containing optional integer `active`, `total`, `limited`, and
`abnormal`. These are sidecar-reported health/version/account counts, not model
backend versions, inferred entitlements, or quota limits. Unknown health keys,
account identities, labels, client UUIDs, hardware, storage/proxy details, raw
errors, request bodies, and credentials are not returned. Healthy `false` from
a valid sidecar response remains a measured degraded state without an error.

Status always uses HTTP 200 for a health outcome so the UI can display a down
sidecar. Failure codes are fixed: `health_unavailable`, `health_http_error`,
`health_invalid_response`, and `health_response_too_large`. Status is uncached.

The only external HTTP destination is the compiled-in
`http://chatgpt2api:80/health?format=json`. A dedicated client ignores environment
and host proxies, refuses redirects, carries no cookie jar or caller headers,
and caps the decompressed response at 1 MiB. There are no network deadlines,
background jobs, explicit retries, credential requests, or admin mutations.
The stable ABI has no per-request cancellation callback: a stalled health
connection can outlive its downstream caller. Shutdown closes idle connections
only; it does not cancel in-flight calls or clear the counter.

The sole public resource is `GET /v0/resource/plugins/chatgpt2api/index`, menu
`ChatGPT2API`, MIME `text/html; charset=utf-8`. Missing HTML gives HTTP 503 with
`dashboard_unavailable`. It cannot expose status or invoke account operations.
Malformed management requests return 400, unknown paths 404, and unsupported
methods on known paths 405 with `Allow: GET` when dispatched to this plugin.
The host may reject unmatched HTTP methods before plugin dispatch.

## Verified Wire And Host Boundary

Contract inspected at `jc01rho/CLIProxyAPIPlus` commit
`5b2785617d1e7de84a9f4dee599d275a4ccd8999` (v7.2.158). Native **C ABI is 1**;
the registration JSON contains top-level **`schema_version: 6`**. The SDK has
no `metadata.rpc_schema_version` field. Lifecycle `config_yaml` is a base64
byte string containing YAML (JSON is accepted as a YAML subset).

Model-route RPC fields are the SDK's PascalCase `RequestedModel`,
`AvailableProviders`, `Handled`, `TargetKind`, `Target`, `TargetModel`, and
`Reason`. A handled decision is `TargetKind: "provider"`,
`Target: "openai-compatible-chatgpt2api"`, `TargetModel: ""`,
`Reason: "chatgpt2api integration"`. Snake-case `target_kind`/`target_model`
would not decode into this SDK and are deliberately not emitted. Management
responses use `StatusCode`, `Headers`, and base64-encoded `Body` bytes; the
`Content-Type` header, not an invented `ContentType` field, carries the MIME.

Pinned host evidence:

- [RPC registration schema](https://github.com/jc01rho/CLIProxyAPIPlus/blob/5b2785617d1e7de84a9f4dee599d275a4ccd8999/internal/pluginhost/rpc_schema.go#L10).
- [Model route SDK contract](https://github.com/jc01rho/CLIProxyAPIPlus/blob/5b2785617d1e7de84a9f4dee599d275a4ccd8999/sdk/pluginapi/types.go#L532).
- [Provider selection preserves original model when TargetModel is empty](https://github.com/jc01rho/CLIProxyAPIPlus/blob/5b2785617d1e7de84a9f4dee599d275a4ccd8999/sdk/api/handlers/handlers_routing.go#L146).
- [Selected provider reaches AuthManager.Execute](https://github.com/jc01rho/CLIProxyAPIPlus/blob/5b2785617d1e7de84a9f4dee599d275a4ccd8999/sdk/api/handlers/handlers_execution.go#L97).
- [Host auth model constraint checks ClientSupportsModel and selection aliases](https://github.com/jc01rho/CLIProxyAPIPlus/blob/5b2785617d1e7de84a9f4dee599d275a4ccd8999/sdk/cliproxy/auth/conductor_selection.go#L940).
- [Existing core provider-route/AuthManager regression test](https://github.com/jc01rho/CLIProxyAPIPlus/blob/5b2785617d1e7de84a9f4dee599d275a4ccd8999/sdk/api/handlers/handlers_model_router_test.go#L605).

The production-core image was exercised in an isolated fixture with this library.
Native alias mapping and both synthetic auth keys were preserved; JSON and SSE
requests reached the fake upstream. A stale allowlisted model was selected by the
plugin but rejected by host auth selection without another upstream POST. Route
counts were `0 -> 3 -> 4 -> 4`, while upstream POST counts were `0 -> 3 -> 3 -> 3`.
The fixture containers, relay and private network were removed afterward.

## Public Deployment

The library is loaded alongside the existing plugins. The Manager menu is at
`https://cliproxy.jclee.me/management.html#/plugin-pages/chatgpt2api/0`.
Public JSON and SSE calls both returned expected replies while the plugin route
count increased from 0 to 2. All 27 existing ChatGPT2API public model IDs remained
available. The explicit routing list contains 25 non-shared IDs; `auto` and
`gpt-image-2` continue through the original host routing.

The native library is installed as `/opt/dashboard/plugins/chatgpt2api.so` and the
static resource as `/opt/dashboard/plugins/chatgpt2api/index.html`. Its enabled
configuration and exact model list are stored in the existing CPA configuration;
no account/key migration or new secret-manager request is needed. A binary update
requires a fresh core process when the host retains the prior native library;
verify the reported plugin version, not just the file's checksum.

This integration does not fix the pre-existing ChatGPT Web empty-image response
problem. A healthy sidecar or increased routing counter is not image-generation
success. No production requests or deployments are performed by the tests.
