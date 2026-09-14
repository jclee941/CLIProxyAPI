# ChatGPT2API Manager Resource

A thin resource inside the existing Manager iframe, with preserved connection
status and explicitly selected GPTWeb account operations. No application shell,
credential/API-key editor, generation controls, or alternative dashboard.

## Build And Verify

From `deploy/chatgpt2api-plugin/web`:

```sh
bun --no-env-file install --frozen-lockfile --ignore-scripts
bun --no-env-file run typecheck
bun --no-env-file run build
bun --no-env-file run test
bun --no-env-file run qa
```

Requires Bun 1.3+, Node 22.18+ (native TypeScript stripping), and local Chrome
stable for Playwright's `channel: 'chrome'`. No saved profile, login, `.env`,
secret, or production connection is required. Dependencies are pinned to the
already-used Gemini resource versions. No new frontend framework is installed.

`build` emits `index.html`, with one bundled inline script and inline CSS.
It is the complete runtime artifact; no source tree or frontend toolchain is
needed in the serving container. There are no runtime CDNs, fonts, images,
stylesheets, analytics, or account/health fixtures in this artifact.

`test` uses Node's built-in test runner for the frozen response contract,
nullable/missing data, invalid integers/arrays/identity, unknown-field removal,
the exact host secureStorage envelope, and V8 validation of the built script.
Account tests also cover safe selector/metric parsing, capability absence,
disabled-source consent, disabled-create versus duplicate results, snapshot
identity mismatch, HTTP 200 body errors, and consent for both toggle directions.

`qa` starts ephemeral loopback-only HTTP servers and isolated real Chrome
contexts, blocks off-origin requests, exercises the built artifact in a
source-matched mock host, and closes all contexts and servers in `finally`.
It checks initial/manual loading, updated counters, focus preservation, 401/403,
upstream-down HTTP 200, malformed data, missing/null account observations,
host auth errors, parent navigation, pagehide cancellation followed by a fresh
manual request, no polling/mutations/redirects, live host theme changes, reduced
motion, and long/HTML-like model names rendered as text.

Each status QA run writes a unique ignored `evidence/status-*/` directory;
`qa-report.json` enumerates scenarios
and screenshots. Required healthy/401/down states cover 375/1280px and both
light/dark. Screenshots are labeled MOCK. To include the complete content,
capture temporarily expands only the mock iframe height, never its width,
and then restores the normal bounded iframe. These are synthetic UI checks,
not claims of production service health.

Optional manual preview, also loopback-only:

```sh
bun --no-env-file run preview:mock healthy
bun --no-env-file run preview:mock auth-error
bun --no-env-file run preview:mock upstream-down
```

Open the printed URL; add `?theme=dark` for the host's dark theme. Stop each
preview with SIGINT/SIGTERM. It serves fixtures only, never a live upstream.
These previews intentionally exercise status only; account API absence is shown
as an actionable error, not empty or ready data. For account operations, use the
actual nested-build harness below.

## Account UI And Nested Build QA

After building the maintained Manager patch, run from this `web/` directory:

```sh
bun --no-env-file run qa:accounts \
  /absolute/patched-manager/vendor \
  /absolute/new-evidence-directory \
  "$PWD/node_modules/@playwright/test/index.mjs"
```

This launches the **actual built Manager and actual built GPT resource** with
Remember off in fresh isolated Chrome, on an ephemeral loopback HTTP fixture.
The fixture key and DTOs are synthetic. Off-origin traffic and service workers
are blocked. No real credentials, production browser, source import, upstream
quota request or enable/disable operation is used. The optional last argument
filters scenario names. Existing evidence directories are refused.

The 20 scenarios cover source/import/quota/toggle at 375/768/1280px in light and
dark; disabled-source two-part approval; keyboard Tab/Escape/focus restoration;
duplicate target preservation; unsupported/missing capability, unconfigured
server key, malformed DTO and unknown state; HTTP 200 refresh failure, HTTP
failure, rotation and ID mismatch; pending state, cancellation, explicit disable;
account 401 versus Manager authentication; exact POST/body denials and sanitized
HTTP 200 errors. `browser.json` records 37 screenshot hashes, exact safe request
bodies, observed synthetic mutations, privacy assertions and cleanup. Only IDs
and consent/enable booleans occur in account request bodies. The browser asserts
that account API traffic originates from the parent, not the iframe, and that
the Manager key is absent from child DOM, globals, logs and decoded persistence.

## Runtime Contract

- Public static resource: `GET /v0/resource/plugins/chatgpt2api/index`.
- Authenticated data: `GET /v0/management/plugins/chatgpt2api/status`.
- Only navigation: `/management.html#/ai-providers`, `target="_top"`, no query.
- One initial status GET plus one safe source GET and one Web inventory GET.
  The status refresh button preserves its original GET behavior. **List reload**
  only reloads safe DTOs; it never invokes a vendor quota refresh.
  One keyless host-ready event may
  retry an initial missing host context, but never an HTTP request failure.
  No repeated-event query loops, intervals, redirects, bulk actions or generation.
- Required plugin/provider/mode/credential identities are validated. Model
  chips and counts come solely from `routing.model_names`; route_count is
  labeled as routing selections, never successful inference requests.
- Only present upstream version/active/total account observations are rendered.
  Null and missing values are not fabricated as zero. Limited/abnormal counts
  are parsed but do not add unrequested dashboard widgets.
- Unknown response fields, including raw `upstream.error`, are discarded.
  `healthy: false` selects fixed local failure copy; outer HTTP errors never
  expose their bodies. Failed status refresh removes the previous status snapshot.

Account endpoints are under `/v0/management/plugins/chatgpt2api`:

| Action | Wire body / behavior |
| --- | --- |
| `GET /codex-sources` | Physical source DTOs only: opaque ID, generated label, disabled flag, `provider:codex` |
| `GET /webaccounts` | Safe Web targets, nullable image counters, timestamps and boolean `capabilities.preserve_disabled_accounts`; no refresh side effect |
| `POST /import-codex` | `{id,consent:true,allow_disabled_source?:true}` after unchecked consent and, for disabled sources, a separate unchecked risk override |
| `POST /refresh-web` | `{id}` for one explicitly selected Web snapshot only |
| `POST /set-web-enabled` | `{id,enabled:boolean,consent:true}` after a separate Web-only confirmation |

Import is access-only **server to server**. No access/ID/refresh token is sent to
or entered in this UI. New targets are always disabled. `already_present` retains
the existing target's current state; it does not imply disabled, ready or newly
created. Codex source files, disabled flags and credential ownership are untouched.
Enable/disable never follows import or refresh automatically. Unknown target
status and unsupported or missing vendor capability disable actions with recovery
guidance. An unconfigured `CHATGPT2API_AUTH_KEY` is an operator-side bootstrap
gate, never a browser input. POSTs recheck support server-side.

Image values are separate: `observed_image_remaining` is the last stored
`conversation/init` image observation, while `tracked_image_remaining` is a
locally maintained vendor counter, **not measured entitlement**. Null or missing
metrics remain unknown, never zero or unlimited. Invalid numeric types fail
closed. `reset_after_seconds` is displayed as response-relative seconds without
inventing a current countdown or reset date. There are no Codex wham values,
percentages, inferred plans or weekly totals. Top-level `observed_at` is inventory
retrieval/completion time, not quota-observation time. Account-level observation
time and `observation_source` are displayed separately. Toggles return stored
snapshots and do not claim freshly measured quota.

HTTP 200 with `refresh_error:vendor_refresh_failed` is a failed observation:
previous quota stays visibly stale. Transport/contract/rotation errors never
replace the selection with a different account; reload the list and reselect.
Uncertain mutations are not retried automatically. Reload/identity errors block
further selected actions pending a list reload. Known account errors have fixed
local copy and do not ask for another login; actual Manager authentication errors
remain Manager-owned.

## Reused Host Contracts

`src/host-auth.ts` is the small tested adapter from
`../../gemini-web-plugin/web/src/auth.ts`, with its import placed first.
The storage and transport semantics are unchanged. This repository's MIT
license covers the reused code; no third-party application source is vendored.

With the maintained Manager patch, the resource first validates the keyless
`__CPAMP_PLUGIN_HOST__` capability for this exact document/URL/plugin. The parent
uses its current native auth transport internally and grants ChatGPT's GET
plugin prefix plus only the three exact POST paths above with bounded opaque-ID
and boolean-only request bodies. Remember-off login works without copying/persisting a
management key or prompting again. Navigation, reload, logout and auth/target
changes revoke and abort the capability. See the [Manager bridge contract](../../cpa-manager-plus/PATCH-BUILD.md).

The unchanged legacy adapter is used only when no valid capability is available.
It reads the same-origin parent's existing `cli-proxy-auth` Zustand envelope.
Plaintext and `enc::v1::` base64/XOR secureStorage formats are supported,
using the exact public host/UA-derived salt. Only `state.managementKey` and
same-origin `state.apiBase` are consumed. The resource never writes storage,
guesses global key aliases, reads React internals, or accepts URL credentials.
Full Manager mode uses the Manager admin context; the core adapter uses the
same supported storage envelope. Manager retains control of authentication.
Absent both transports or an actual Manager 401 directs users back to Manager,
not a second key form. A broker 403 gives patch/permission guidance. The genuine
ChatGPT Web account pool stays in the existing service.

`build.ts` reads the existing Gemini `tokens.css`, and the UI imports only its
small typed DOM primitives. The repository sibling must therefore be present
at build time, but not at runtime. Host-injected CSS tokens override fallback
tokens via the same low-priority CSS layer. See `../DESIGN.md` for the preserved
visual contract and explicit task boundaries.

## Diagnostics And Boundaries

Strict `tsc --noEmit`, Node checks, and real-browser scenarios are the executable
gates. The shared LSP intermittently times out on fresh TypeScript diagnostics;
the final directory scans covered all 12 source and 12 test TypeScript files
with zero errors. Manager policy and browser harness scans also passed.
Applying
the generic Biome LSP to generated `index.html` reports minifier/library style
rules such as assignments in expressions. The artifact is not hand-edited and
no lint rule is disabled; its JavaScript is checked by V8 and real Chrome.
The optional shared skill checker requires TypeScript 7's unstable API, which
the repository's pinned TypeScript 5.9.3 does not expose. It was not used as a
passing gate and the repository toolchain was not upgraded to accommodate it.

This task owns `web/**`, plugin `DESIGN.md`, and the narrow ChatGPT addition to
the maintained Manager patch/tests/provenance. No Gemini source or artifact is
changed; its full compatibility build/tests/QA ran in a unique disposable copy.
Backend Go and the plugin-root API README are separately owned. Core, deployment
configuration, real credentials, production browsers, SSH and production account
operations are untouched. See [PATCH-EVIDENCE.md](../../cpa-manager-plus/PATCH-EVIDENCE.md)
for the combined patch hash, fresh replay, unchanged baseline files and review.
Live scoped host callbacks, bootstrap environment injection, homogeneous patched
vendor rollout and real Web entitlements remain unverified operator gates.
No commit, push, deployment, OCI build or Lighthouse certification is claimed.
