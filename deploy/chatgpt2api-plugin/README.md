# ChatGPT2API Native Integration Plugin

`chatgpt2api` version `1.0.1` is a routing/management plugin that **delegates
inference to the existing native provider engine**. It is not a rewritten
ChatGPT backend. It registers only `model_router` and `management_api`.
There is no executor, model registrar/provider, auth provider, automatic account
migration, or core patch in this module. Explicit management operations can read
one consented Codex access-credential snapshot through scoped host callbacks.

**New-account import, disabled-target quota refresh, and Web enable/disable require vendor patch
[`0005-preserve-disabled-accounts.patch`](../chatgpt2api/0005-preserve-disabled-accounts.patch).**
Apply it after patches `0001` through `0004` on vendor commit
`e55aef2829e7bf1d7256d6ff3feb4b40b02743d2`. The plugin checks the versioned
capability before sending a new credential, imports in a single disabled-create
operation, and never follows import with a second disable request. Unpatched
vendors still return the fixed unsupported errors without receiving a POST.

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
api_base_url: http://chatgpt2api:80
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

The status health HTTP destination remains the compiled-in
`http://chatgpt2api:80/health?format=json`. A dedicated client ignores environment
and host proxies, refuses redirects, carries no cookie jar or caller headers,
and caps the decompressed response at 1 MiB. There are no network deadlines,
background jobs, explicit retries, credential requests, or admin mutations in
the status path. The separate account API is described below.
The stable ABI has no per-request cancellation callback: a stalled health
connection can outlive its downstream caller. Shutdown closes idle connections
only; it does not cancel in-flight calls or clear the counter.

The sole public resource is `GET /v0/resource/plugins/chatgpt2api/index`, menu
`ChatGPT2API`, MIME `text/html; charset=utf-8`. Missing HTML gives HTTP 503 with
`dashboard_unavailable`. It cannot expose status or invoke account operations.
Malformed management requests return 400, unknown paths 404, and unsupported
methods on known paths 405 with `Allow: GET` when dispatched to this plugin.
The host may reject unmatched HTTP methods before plugin dispatch.

## Codex Sources And Web Quota API

All five account routes are under **`/v0/management/plugins/chatgpt2api`**, protected by
the existing host management middleware. They are not resource routes. No
browser-supplied credentials, headers, source JSON, file paths, or URLs are
accepted. Every request requires the current host-injected `host_callback_id`;
it is forwarded unchanged to `host.auth.list/get_runtime/get`, never cached,
minted by the plugin, or read from the browser body. Missing callback context
fails with HTTP 503 `host_callback_required`. Host callback failures are fixed
errors, never forwarded messages. No host auth save, refresh, enable, or update
callback is used.

The private account API reads `CHATGPT2API_AUTH_KEY` from the **host process
environment**, injected by the operator at process bootstrap. It is not a
plugin config field, an inference key, a Manager key, or an `info` response
field. No per-request 1Password access, Core-config scraping, container-env
lookup, or fallback exists. An absent/invalid value yields HTTP 503
`web_api_unconfigured_set_CHATGPT2API_AUTH_KEY`. The current operator environment
has not been modified or verified by this implementation.

`api_base_url` is trusted operator-only configuration. It accepts the exact
default `http://chatgpt2api:80`, HTTPS origins, and HTTP IP-loopback origins for
fixtures. Userinfo, paths other than `/`, query strings, and fragments are
rejected. Operators must select a trusted private destination and network.
The dedicated HTTP client does not use host HTTP callbacks, environment proxies,
redirects, caller headers, cookies, or post-connect deadlines. Decoded responses
are bounded to 4 MiB. Cancellation is propagated where a Go context is available;
C ABI 1 has no downstream cancellation callback. A manual refresh waits for its
vendor progress result, polling every 250 ms only within that explicit call.
There is no background mirror or scheduled quota refresh. A stuck vendor job
can keep an ABI call open; cancelling a caller does not cancel a vendor job
already accepted. Shutdown only closes idle HTTP connections.

Config decoding is strict: unknown keys (including credentials) are rejected.
Host-reserved `enabled: boolean` and `priority: integer` are accepted without
changing their host-owned semantics. Invalid reloads preserve prior settings.

### Exact Public DTOs

The following TypeScript notation documents the JSON wire contract only; it
does not add frontend code. All listed nullable fields are present as `null`
when unknown, rather than fabricated zeroes. `type` alone is optional.

```ts
type CodexSourceID = string; // "codex_" followed by 64 lowercase hex characters
type WebAccountID = string;  // "web_" followed by 64 lowercase hex characters
type Timestamp = string;     // RFC 3339 UTC

type CodexSource = {
  id: CodexSourceID;
  label: string;
  disabled: boolean;
  provider: "codex";
};

type SafeWebAccountView = {
  id: WebAccountID;
  label: string;
  disabled: boolean;
  status: "disabled" | "normal" | "limited" | "abnormal" | "unknown";
  source_type: "web" | "codex" | "oauth_login" | "cpa" | "sub2api" | null;
  type?: "free" | "Free" | "plus" | "Plus" | "pro" | "Pro"
    | "prolite" | "ProLite" | "team" | "Team" | "business" | "Business"
    | "enterprise" | "Enterprise";
  tracked_image_remaining: number | null;
  observed_image_remaining: number | null;
  reset_after_seconds: number | null;
  observed_at: Timestamp | null;
  observation_source: "stored_snapshot" | "conversation/init";
  refresh_error: "vendor_refresh_failed" | null;
};

// GET /codex-sources
type CodexSourcesResponse = { sources: CodexSource[] };

// GET /webaccounts, no vendor refresh side effect
type WebAccountsResponse = {
  accounts: SafeWebAccountView[];
  observed_at: Timestamp;
  source: "ChatGPTWeb";
  capabilities: { preserve_disabled_accounts: boolean };
};

// POST /import-codex
type ImportCodexRequest = {
  id: CodexSourceID;
  consent: true;
  allow_disabled_source?: true;
};
type ImportCodexResponse = {
  status: "already_present" | "imported_disabled";
  account: SafeWebAccountView;
};
// Unpatched vendors return 409 {error:"vendor_disabled_import_unsupported"}.

// POST /refresh-web
type RefreshWebRequest = { id: WebAccountID };
type RefreshWebResponse = {
  account: SafeWebAccountView;
  observed_at: Timestamp;
  source: "ChatGPTWeb";
};

// POST /set-web-enabled
type SetWebEnabledRequest = {
  id: WebAccountID;
  enabled: boolean;
  consent: true;
};
type SetWebEnabledResponse = {
  account: SafeWebAccountView;
  source: "ChatGPTWeb";
  observed_at: Timestamp;
};

type AccountAPIError = { error: string }; // fixed codes below, never vendor text
```

`allow_disabled_source` is effective only with explicit `consent: true` and
never changes the source flag. Omitted/false override does not permit a disabled
source. Bodies are bounded to 4 KiB, reject unknown fields, and accept exactly
one opaque ID, not an email, filename, batch, or access token. Source credentials
are bounded to 32 KiB strings with no control characters; storage type must be
exactly `codex`. An optional source `id_token` is validated but not forwarded;
the import needs only the access snapshot. `refresh_token` is not declared
in the source DTO, forwarded, persisted, or used, even as an empty field.

Source selection hashes the exact listed physical ID plus `AuthIndex`, then
resolves only that index. Runtime identity/provider/index/name and physical
JSON type are cross-checked. Runtime-only, unavailable, non-Codex, absent-index,
and non-file entries are not offered. Labels are intentionally generated
(`Codex <digest prefix>`, `Web <digest prefix>`), not copied from credential-rich
metadata, filenames, or email fields. Source files and runtime flags are never
written by this plugin.

Web IDs are domain-separated SHA-256 hashes of the current access-token
snapshot, not credentials or stable person identifiers. The vendor's records
do not provide an immutable per-credential record ID; `user_id`/email cannot
uniquely identify multiple snapshots. Rotation changes the Web ID. Relist and
reselect manually. Exact token equality is the only duplicate match. A different
token with the same email is not merged. Existing duplicates return their current
safe view without POST, preflight, status change, metadata merge, or refresh-owner
change. No persistent CPA token copies or email-based association table exist.

`tracked_image_remaining` is the vendor's stored `quota` counter, which it can
decrement locally after generation and initialize itself. It is not a fresh
entitlement measurement. `observed_image_remaining` is exclusively the stored
last `limits_progress` entry for `feature_name == "image_gen"`, originating from
`conversation/init`. Invalid/missing/noninteger counters and ambiguous duplicate
metrics are null. The vendor UI treats `reset_after`/`restore_at` as an absolute
date. Only a valid RFC 3339 `image_gen.reset_after` is converted to nonnegative
seconds relative to this response's observation time. Bare numeric reset values,
unknown date formats, and missing values remain null; no unit guess is made.

Top-level `observed_at` timestamps retrieval/completion, not the age of stored
quota. Individual `observed_at` is null on listing and duplicates because the
vendor does not persist a quota-observation timestamp. Only a completed,
successful explicit refresh sets it and `observation_source: "conversation/init"`.
A vendor-reported per-account refresh failure returns HTTP 200 with the stored
view, `refresh_error: "vendor_refresh_failed"`, and no fresh-observation claim.
Transport/progress failures return a fixed HTTP error, not an old success DTO.
Plan `type` and `source_type` are emitted only from recognized vendor record
values. Unknown plan types are omitted. There are no invented PRO entitlements,
percentages, windows, or limits. `normal` means the vendor status, not a plugin
claim that Web inference is usable. `unknown` must not be interpreted as enabled.

Refresh sends only the selected current snapshot in
`POST /api/accounts/refresh {access_tokens:[<private snapshot>],preserve_disabled:true}`
on a patched vendor (the flag is omitted for legacy non-disabled refresh), and consumes
`GET /api/accounts/refresh/progress/<validated UUID>`. Only the vendor's existing
refresh owner can rotate its stored credentials. No source credential refresh
or second owner is introduced. If the selected token is absent after completion,
HTTP 409 `web_account_stale_reload_needed` requires reload; no account is guessed
by email or user ID. Unknown-status targets are rejected before POST. Disabled
targets require the capability. A protected metadata merge checks the current
disabled status inside the vendor account lock, including a disable performed
while metadata is being fetched. Existing token rotation, invalid-token error
recording, and configured invalid-token deletion remain available. Protected
password recovery also preserves disabled status at its final update.
The patched refresh endpoint publishes its queued progress record before
returning the job ID, so immediate polling cannot race worker initialization.

### Explicit Web Enable/Disable

`POST /set-web-enabled` is a separate, explicit operator action on one existing
Web snapshot, never an import side effect or bulk activation. The body requires
all three exact, case-sensitive keys: `id`, `enabled` (a JSON boolean), and
`consent` (literal `true`). Missing, null, unknown, duplicate, incorrectly cased,
and incorrectly typed fields are rejected with HTTP 400
`invalid_web_enabled_request_require_consent`. The existing 4 KiB body bound
applies. Browser credentials, URLs, source IDs, and metadata overrides are not
accepted.

The route verifies the current host callback scope through `host.auth.list`,
but never reads source credential JSON or invokes a runtime/source mutation.
It loads fresh private Web inventory and requires numeric capability version
`preserve_disabled_accounts: 1` for either direction, including no-ops.
Unsupported vendors return HTTP 409 `vendor_web_enabled_unsupported` before
any POST. An absent or rotated Web ID returns
`web_account_stale_reload_needed`; unknown vendor status returns
`web_account_status_unknown`. Duplicate access-token records are invalid
inventory, not candidates to guess between.

For an actual state change, the sole vendor mutation is authenticated
`POST /api/accounts/update` with exactly `access_token` (selected server-side)
and `status`: `正常` for enable, `禁用` for disable. No quota, plan type, restore
time, source type, proxy, or credential-owner fields are submitted. The vendor
merges these fields into the existing record. Source Codex flags, bytes, and
refresh ownership remain untouched; the source can stay disabled.

Already-disabled disable and already-non-disabled enable return the current
safe view without POST. Known `limited` and `abnormal` states count as
non-disabled for this no-op check and are preserved, not repaired to `normal`.
After a mutation, both returned `item` and current `items` must identify the
exact selected access snapshot and report the requested status. Missing or
rotated identity returns HTTP 409 `web_account_stale_reload_needed`; conflicting
statuses return HTTP 502 `vendor_web_enabled_contract_violated`. There are no
retries, compensating writes, or email/user-ID guesses. An error after POST does
not prove the vendor did not apply a change; reload before choosing another action.

Success uses the exact `SetWebEnabledResponse` wrapper above. Its top-level
`observed_at` is retrieval/completion time, while the account remains a
`stored_snapshot` with nullable account-level `observed_at` and existing refresh
errors preserved. This action performs no quota refresh. `normal` is observed
vendor status only, not proof of usable Web entitlement or remaining quota.

### Fixed Account Error Codes

| HTTP | Codes |
| --- | --- |
| 400 | `invalid_management_request`, `invalid_account_request`, `invalid_import_request_require_consent`, `invalid_web_account_id`, `invalid_web_enabled_request_require_consent` |
| 404 | `source_not_found`, `not_found` |
| 405 | `method_not_allowed` with route-specific `Allow` |
| 409 | `source_metadata_unavailable`, `source_credential_invalid`, `disabled_source_requires_override`, `vendor_disabled_import_unsupported`, `vendor_disabled_refresh_unsupported`, `vendor_web_enabled_unsupported`, `web_account_status_unknown`, `web_account_stale_reload_needed` |
| 502 | `web_api_unavailable`, `web_api_http_error`, `web_api_redirect_blocked`, `web_api_invalid_response`, `web_api_response_too_large`, `vendor_refresh_failed`, `vendor_disabled_contract_violated`, `vendor_web_enabled_contract_violated` |
| 503 | `host_callback_required`, `host_auth_unavailable`, `web_api_unconfigured_set_CHATGPT2API_AUTH_KEY`, `request_cancelled` |
| 500 | `host_request_encoding_failed`, `request_encoding_failed`, `response_encoding_failed` |

All account responses use `Cache-Control: no-store`. The standard `/status`
shape, static resource behavior, model aliases, model versions, and exact two
capabilities are unchanged.

### Pinned Vendor Safety Contract

Inspected public `basketikun/chatgpt2api` revision
`e55aef2829e7bf1d7256d6ff3feb4b40b02743d2`, not any runtime account store:

- [`api/accounts.py:215`](https://github.com/basketikun/chatgpt2api/blob/e55aef2829e7bf1d7256d6ff3feb4b40b02743d2/api/accounts.py#L215): creation upserts by access token, then unconditionally refreshes submitted tokens.
- [`services/account_service.py:206`](https://github.com/basketikun/chatgpt2api/blob/e55aef2829e7bf1d7256d6ff3feb4b40b02743d2/services/account_service.py#L206): normalization accepts the actual disabled string `禁用`; English `disabled` is not the scheduler's disabled encoding.
- [`services/openai_backend_api.py:332`](https://github.com/basketikun/chatgpt2api/blob/e55aef2829e7bf1d7256d6ff3feb4b40b02743d2/services/openai_backend_api.py#L332): successful preflight returns `正常`/`限流`, replacing disabled.
- [`services/account_service.py:1335`](https://github.com/basketikun/chatgpt2api/blob/e55aef2829e7bf1d7256d6ff3feb4b40b02743d2/services/account_service.py#L1335) and [`1216`](https://github.com/basketikun/chatgpt2api/blob/e55aef2829e7bf1d7256d6ff3feb4b40b02743d2/services/account_service.py#L1216): preflight merges that status into the current account without a disabled-state guard.
- [`api/accounts.py:249`](https://github.com/basketikun/chatgpt2api/blob/e55aef2829e7bf1d7256d6ff3feb4b40b02743d2/api/accounts.py#L249): manual quota refresh is asynchronous and returns a progress ID, not account records immediately.
- [`web/src/app/accounts/page.tsx:107`](https://github.com/basketikun/chatgpt2api/blob/e55aef2829e7bf1d7256d6ff3feb4b40b02743d2/web/src/app/accounts/page.tsx#L107): stored restore values are interpreted as absolute dates.

A tests-only run against the pin plus `0001` through `0004` observed `禁用`
being persisted initially and then overwritten by successful preflight. The
same actual HTTP-handler and JSON-storage tests pass with `0005`, including a
concurrent operator disable, rate-limited metadata, duplicate no-write, token
rotation/recovery, and invalid-token deletion/error handling. No runtime host
access, real credentials, real imports, real quota calls, restart, or deployment
was performed.

The authenticated vendor `GET /api/accounts` now includes this non-secret marker
alongside its existing private `items` array:

```json
{"capabilities":{"preserve_disabled_accounts":1}}
```

Only numeric version `1` is supported. Missing, malformed, or other versions do
not authorize a new import, disabled refresh, or Web enable/disable. The marker is checked on the
fresh inventory for every operation, not cached. The public `GET /webaccounts`
projects only `{ "capabilities": { "preserve_disabled_accounts": true } }`
(false when unsupported), in addition to its existing safe account DTOs. UI
readiness is informational; POST authorization always rechecks vendor support.

The exact private import body is:

```json
{"preserve_disabled":true,"accounts":[{"access_token":"<private snapshot>","status":"禁用","source_type":"codex"}]}
```

This protected mode requires one disabled account, rejects refresh-token and
other credential-owner fields, and checks exact-token duplicates under the
existing account lock before any write. A concurrently inserted duplicate is
also returned unchanged with `already_present`, without preflight. New records
are disabled at their first persistence and stay disabled through preflight.
Metadata is still updated. `imported_disabled` is emitted only after the vendor
returns the exact token with disabled status. It does not claim fresh quota or
inference readiness. A contradictory vendor response returns
`vendor_disabled_contract_violated`, never a fabricated disabled DTO or repair
POST. A deleted/missing token requires relisting.

`preserve_disabled` is operation-scoped, not a new account-schema field. The
vendor also uses `禁用` for deactivated-account health failures, so legacy
operations keep their existing health-repair behavior. This is not a fence
against unrelated legacy refreshes, explicit operator enables, or multiple
independent vendor processes sharing a store. Deploy a homogeneous patched
vendor behind the configured origin; do not mix patched and unpatched replicas
between capability GET and mutation POST.

Enabling remains a separate explicit operator action through the plugin's
`POST /set-web-enabled` or the existing vendor account editor. The pinned
[`api/accounts.py` update handler](https://github.com/basketikun/chatgpt2api/blob/e55aef2829e7bf1d7256d6ff3feb4b40b02743d2/api/accounts.py#L329)
accepts only supplied non-null update fields and returns `item` plus `items`;
the service merges them under its account lock. Its existing token-alias
resolution can follow a rotation between inventory GET and update POST. The
plugin detects a changed returned snapshot and reports stale/reload, but the
vendor has no conditional-update API to prevent that race before persistence.
This action does not refresh or enable the source Codex account and must not
be scripted as the second half of import.

Run these in a disposable patched vendor checkout after `uv sync --frozen --offline`:

```sh
.venv/bin/python -B -m test.run_preserve_disabled_offline
.venv/bin/python -B -m test.run_preserve_disabled_offline --manual
.venv/bin/python -B -m test.run_pro_pipeline_offline
.venv/bin/python -B -m test.run_image_poll_offline
```

The runners isolate configuration/storage and use synthetic credentials. Unit
mode blocks socket/curl-cffi calls; `--manual` starts and stops a loopback-only
HTTP server, mocks remote metadata I/O, and blocks outgoing curl-cffi calls.
Do not run vendor live-account smoke modules.
Production host callback scope, bootstrap environment injection, homogeneous
vendor rollout, and actual Web entitlement remain operator/runtime gates.

Local tests cover the fixed routes, exact two capabilities/schema 6, consent,
source/runtime/provider boundaries, file byte/mode/mtime preservation, exact-token
duplicate no-POST, capability-gated disabled import, credential-rich DTO redaction, nullable
metrics/reset units, manual refresh success/failure/rotation, disabled refresh
preservation/unsupported rejection, redirect blocking, response bounds, and operator-key absence. The
`abismoke` suite passes a synthetic C host callback through the actual exported
ABI entrypoints to a loopback private HTTP fixture and verifies matching host
allocation/free counts. Production-host auth scope enforcement, operator env
injection, and actual vendor Web entitlement remain unverified here.

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

The following historical routing evidence predates the account API changes; it
does not validate the new account routes against a running production host.
The production-core image was exercised in an isolated fixture with the earlier library.
Native alias mapping and both synthetic auth keys were preserved; JSON and SSE
requests reached the fake upstream. A stale allowlisted model was selected by the
plugin but rejected by host auth selection without another upstream POST. Route
counts were `0 -> 3 -> 4 -> 4`, while upstream POST counts were `0 -> 3 -> 3 -> 3`.
The fixture containers, relay and private network were removed afterward.

## Existing Public Deployment (Historical)

This section records the earlier routing integration. The new account API
artifact has not been deployed, and no account operations were run in production.

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
