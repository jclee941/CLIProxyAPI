# Gemini Web Manager Resource

A self-contained resource page for the existing CPA-Manager-Plus plugin iframe.
This is not a Manager fork or a standalone management dashboard. The host keeps
its navigation, header, authentication, and theme.

## Build And Verify

Run these commands from `deploy/gemini-web-plugin/web`:

```sh
bun --no-env-file install --frozen-lockfile --ignore-scripts
bun --no-env-file run typecheck
bun --no-env-file run build
bun --no-env-file run test
bun --no-env-file run qa
```

Requirements: Bun 1.3+, Node.js (the generated-script syntax check), and a local
Chrome stable installation available to Playwright's `channel: 'chrome'`.
No extra browser profile, account login, environment file, or secret is needed.
The commands deliberately disable Bun's automatic `.env` loading.

`build` produces **`web/index.html`**, containing the bundled JavaScript, CSS,
and local SVG paths. It has no runtime CDN, font, analytics, script, or stylesheet
dependency. This generated artifact is included so the backend can serve it
without a frontend toolchain. Rebuild it after changing `src/`.

`test` runs unit/artifact, login-contract and external-port checks, including the host's actual obfuscation
format, same-origin checks, opaque token format, nullable Google observations,
and V8 syntax validation of the built inline script. Build before running tests.
Authored TypeScript is verified with the strict project typecheck. Running the
generic Biome LSP directly on generated `index.html`
reports style diagnostics in minified/library code (assignment expressions,
comma operators, and similar bundler output). These are not suppressed or
hand-edited; the generated artifact is checked by V8 and the real-browser suite.

`qa` starts ephemeral loopback-only MOCK HTTP servers and isolated Chromium
contexts. It exercises the built HTML through a local, source-matched mock host
iframe. All test-visible account labels are marked **MOCK**. It blocks requests
outside the local mock origin, checks that key/token values are not rendered,
and closes every server and browser context in `finally` blocks.

The original 18 browser scenarios remain intact: light/dark at 375/768/1280px; dynamic account
counts (0, 1, 5, 6); available model filtering; actual fraction-derived usage;
null/unknown/AI-credit quotas; expired tokens; host authentication failures;
blocked storage; registration/replacement; masked/cleared token input; keyboard
focus, Tab containment and Escape; partial refresh failure; a two-request
concurrency ceiling; no follow-up list GET during refresh; no timed polling;
live host theme changes; reduced motion; and safe rendering of long/HTML-like
account labels.

Additional scenarios drive the real portal DOM with a fake Chrome runtime
injected only at the external-port boundary. They cover consent, absent companion,
wrong state/port/identity, duplicate sessions, ack, configuration/API errors,
uncertain completion, expiry, cancel during processing, disabled saves, explicit
reconciliation, and privacy. Refresh tests cover HTTP 200 error/expired bodies,
retained stale observations and recovery only after a ready response.
Service workers and non-mock network destinations are blocked.

Each run creates a unique ignored `web/evidence/companion-*/` directory.
Its `qa-report.json` enumerates screenshots and records outcomes and cleanup;
earlier baseline and failed-run evidence is preserved. Full-list
captures temporarily expand only the mock host iframe height to show every
account without clipping; viewport width is unchanged and the normal bounded
host layout is restored afterward. Dialog captures use the normal 1000px-high
viewport. These are mock UI checks, **not proof of production CPA/Google access**.

## Google Login Companion

Use the toolbar's **Google login** to add an account or the corresponding card
action to replace a selected account's session. The portal requires an account
label and explicit, initially unchecked storage consent. After start, connect the
server-approved companion. Its own popup asks the user to select one Gemini
tab/account and approve transfer. Google login and 2FA stay in Google UI: this is
not a Google OAuth token issuer or a webpage password form. Legacy masked token
entry remains separately labelled **Manual token registration**.

Login API paths are relative to `/v0/management/plugins/gemini-web`:

- `POST /login/start`: `{label,existing_id?,consent:true}`.
- `POST /login/complete`: one approved `{state,token,account_sha256,auth_user,extension_id,consent:true}`.
- `POST /login/status`, `/login/cancel`, `/login/reconcile`: `{state}`.

Start/connect require HTTPS (loopback HTTP only for QA), the exact `manager_origin`
returned by start, and resource path `/v0/resource/plugins/gemini-web/index`.
Only the existing host-auth adapter supplies Manager credentials. The extension
ID comes from the authenticated start response, not user input. The sole extension
transport is `chrome.runtime.connect(extension_id, {name:'gemini-web-login'})`.
The portal sends `begin`, accepts `ready` then one matching `session` from that
exact port and state, transfers it once, sends `ack`, and disconnects. Cancel sends
`cancel` before disconnect. No Manager key goes to the extension, and it does not
call Manager HTTP. There is no window-message fallback, native OAuth RPC, automatic
profile capture or manual-token fallback in this flow.

Tokens exist only in the local handoff call; parsed/session payload references
are released/reset afterward. State and credentials never enter rendered text,
URLs, browser storage, downloadable files or application logs. Unknown errors
render fixed safe instructions. Missing companion instructions explain obtaining
the administrator-approved build, enabling it in desktop Chrome, and checking the
exact Manager origin and extension ID. No install URL or extension ID is guessed.

Pending, processing and host-sync pending are not success. Backend
`status:'ready' && models_ready:true` is necessary but not sufficient: the portal
also requires an enabled ready account and a nonempty authenticated host
`GET /v0/management/auth-files/models?name=<account_id>` result. Empty/failed
registry reads remain stored/registration-unconfirmed; explicit Check Status
can recheck once without replaying a token. `saved` alone is unconfirmed.
An account known to be disabled from the account DTO is labelled
exactly **Saved, disabled** and is never enabled automatically. Ready/saved results
perform one account-list read for authoritative accounts and disabled state.
The login-request deadline is never presented as a credential expiry time.

The request deadline is bounded to ten minutes. No quota/status polling is added.
Users explicitly check status or reconcile pending host sync. Uncertain completion
never retransmits a token. Cancellation during processing can still return a
committed or host-sync-pending result; it never promises to delete stored secrets.
Unload disconnects the companion and leaves expiry to the server. In-memory flow
state is not restored from browser storage.

Official references: [Chrome runtime Port](https://developer.chrome.com/docs/extensions/reference/api/runtime#type-Port)
and [web-page messaging](https://developer.chrome.com/docs/extensions/develop/concepts/messaging#external-webpage).
Caller-side ports do not require `sender`; exact source-port object equality is
checked. `runtime.lastError` is read inside disconnect callbacks without logging.

## Host Integration

- Plugin ID: `gemini-web`; menu: `Gemini Web`.
- Resource: `GET /v0/resource/plugins/gemini-web/index` serves `web/index.html`.
- Manager route: `/plugin-pages/gemini-web/0` inside the host router.
- APIs: same-origin `/v0/management/plugins/gemini-web/accounts` and `/refresh`.
- Login: authenticated `/login/*` endpoints above, not native OAuth RPC.
- No resource request needs a management key. The artifact contains no account
  data or credentials. Only subsequent authenticated API calls retrieve data.

The auth adapter is grounded in CPAMP v1.12.11, commit
`e1a8788ab796f4d001c5d1e9851c418989b05424`:

- `apps/web/src/stores/useAuthStore.ts`
- `apps/web/src/services/storage/secureStorage.ts`
- `apps/web/src/utils/encryption.ts` and `utils/constants.ts`
- `apps/web/src/services/api/client.ts`
- `apps/web/src/features/plugins/PluginResourcePage.tsx` and `pluginHostStyle.ts`

With the maintained Manager patch, `src/host-request.ts` validates the keyless,
document-bound `__CPAMP_PLUGIN_HOST__` capability and prefers its request function.
The parent uses current native authentication internally. Remember-off login
works without sending the management key to the iframe or companion and without
new storage. One ready event may retry an initial missing host context; repeated
events never poll. Logout, auth/target changes and navigation revoke the parent
capability and abort its requests. See the [Manager bridge contract](../../cpa-manager-plus/PATCH-BUILD.md).

Only when the capability is absent, the legacy explicit remembered-login or
same-origin standalone path reads `cli-proxy-auth`, decodes `enc::v1::`, and uses
the persisted `state.managementKey`. This adapter is unchanged; neither path
guesses key aliases, reads React internals, writes storage or asks for another
key. If neither is available the existing safe authentication error is preserved.
Full Mode still uses Manager's admin context; the distinct CPA key stays server-side.
An account read and its model check retain one transport choice to avoid switching
to a newly installed capability before the parent observed that account response.

The CPAMP bridge supplies live CSS custom properties. Source-derived fallbacks
are in a lower-priority CSS layer, so they cannot override the injected host
tokens. See [`../DESIGN.md`](../DESIGN.md) for the source-backed visual contract.

## Data And Request Semantics

The server is authoritative for profiles, status, models, tier, usage, and token
validity. No account count, plan entitlement, quota, or successful registration
is manufactured. Expired/error/disabled accounts do not advertise available
models. Enabled ready accounts additionally load their actual public host model
registry; missing registration is unconfirmed and exposes no usable model chips.
Missing usage is visibly unknown, never unlimited or zero.

Usage is accepted only as `source: 'GoogleWeb'`, `estimated: false`, with
`provider_compute_unit` units. Bars use `usage_fraction`; optional
`usage_percent` is not used as a substitute. Remaining values are compute units,
not tokens. Unix-second timestamps are formatted in the browser's local timezone.
Nullable values, including the current backend's nullable metric array, remain
unknown. Currently absent `overage_enabled`/`metric_type` metadata is accepted
without guessing; the full coordinator-specified shape is supported when sent.

Manual refresh uses at most two workers. The current backend returns a complete
account-view object from `POST /refresh`; its identity is validated and the
matching card updates immediately. There is no extra list GET after refresh,
because the backend's list operation itself checks Google. Registration returns
only `{id,status}`, so a successful registration performs one list GET to obtain
the authoritative updated account list.

HTTP 200 alone is not refresh success: a body with `status:'ready'` and confirmed
host model registration is required for an enabled account.
Error/expired/unknown bodies retain the previous observation as stale, hide old
model availability, and count as failures in refresh-all. Failure badges remain
during another request and clear only after a ready response. Tier is an opaque
Google-reported string, without Pro normalization. The account DTO currently has
no auth-source, session-refresh or credential-expiry fields; these remain unknown
rather than being inferred from `observed_at`.

Token input is a required masked textarea, validated only for an opaque
`gemini-web:v1:` base64url envelope. Cookie contents are not decoded client-side.
The input clears on submit, success, failure, cancel, and Escape. Token requests
are never retried automatically. Closing a submitting dialog does not cancel a
server-side mutation; the page reports its eventual result.

Known safe backend error codes distinguish expired Google credentials from a
Manager 401/403 and explain disabled-account update conflicts. Unrecognized raw
error response bodies are never displayed. Redirects and off-origin connections
are rejected. There are no polling timers, automatic disable/delete/renewal
operations, or unsupported chat/video actions.

## Ownership And Remaining Boundary

Only `web/**` and `../DESIGN.md` belong to this frontend task. Backend Go files,
Manager deployment configuration, production browsers, Google profiles, and
1Password are untouched. The coordinator owns final integration against the
actual Manager/CPA resource route and real accounts. No production deployment,
commit, push, production integration or Lighthouse score is claimed here.
