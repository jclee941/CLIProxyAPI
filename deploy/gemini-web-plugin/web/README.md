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

`test` runs 20 unit/artifact checks, including the host's actual obfuscation
format, same-origin checks, opaque token format, nullable Google observations,
and V8 syntax validation of the built inline script. Build before running tests.
Authored TypeScript, CSS, configuration, and HTML templates have clean LSP
diagnostics. Running the generic Biome LSP directly on generated `index.html`
reports style diagnostics in minified/library code (assignment expressions,
comma operators, and similar bundler output). These are not suppressed or
hand-edited; the generated artifact is checked by V8 and the real-browser suite.

`qa` starts ephemeral loopback-only MOCK HTTP servers and isolated Chromium
contexts. It exercises the built HTML through a local, source-matched mock host
iframe. All test-visible account labels are marked **MOCK**. It blocks requests
outside the local mock origin, checks that key/token values are not rendered,
and closes every server and browser context in `finally` blocks.

The 18 browser scenarios cover light/dark at 375/768/1280px; dynamic account
counts (0, 1, 5, 6); available model filtering; actual fraction-derived usage;
null/unknown/AI-credit quotas; expired tokens; host authentication failures;
blocked storage; registration/replacement; masked/cleared token input; keyboard
focus, Tab containment and Escape; partial refresh failure; a two-request
concurrency ceiling; no follow-up list GET during refresh; no timed polling;
live host theme changes; reduced motion; and safe rendering of long/HTML-like
account labels.

Evidence is written under ignored `web/evidence/`. `qa-report.json` enumerates
all 28 final screenshots and records scenario outcomes and cleanup. Full-list
captures temporarily expand only the mock host iframe height to show every
account without clipping; viewport width is unchanged and the normal bounded
host layout is restored afterward. Dialog captures use the normal 1000px-high
viewport. These are mock UI checks, **not proof of production CPA/Google access**.

## Host Integration

- Plugin ID: `gemini-web`; menu: `Gemini Web`.
- Resource: `GET /v0/resource/plugins/gemini-web/index` serves `web/index.html`.
- Manager route: `/plugin-pages/gemini-web/0` inside the host router.
- APIs: same-origin `/v0/management/plugins/gemini-web/accounts` and `/refresh`.
- No resource request needs a management key. The artifact contains no account
  data or credentials. Only subsequent authenticated API calls retrieve data.

The auth adapter is grounded in CPAMP v1.12.11, commit
`e1a8788ab796f4d001c5d1e9851c418989b05424`:

- `apps/web/src/stores/useAuthStore.ts`
- `apps/web/src/services/storage/secureStorage.ts`
- `apps/web/src/utils/encryption.ts` and `utils/constants.ts`
- `apps/web/src/services/api/client.ts`
- `apps/web/src/features/plugins/PluginResourcePage.tsx` and `pluginHostStyle.ts`

It reads only the existing same-origin `cli-proxy-auth` Zustand envelope,
decodes the host's `enc::v1::` reversible obfuscation, and sends the persisted
`state.managementKey` in `Authorization: Bearer ...`. Full Mode uses Manager's
admin context; Manager substitutes its saved CPA key server-side. The resource
never retrieves or needs that separate CPA key. The same storage/transport
contract was verified in official CPAMC source; no guessed storage aliases,
React internals, parent globals, or cross-origin transports are supported.

When Manager does not remember the login, its persisted envelope deliberately
omits the key. The resource explains the saved-login requirement and offers one
explicit reconnection action. It never asks for a second management key or
creates a new localStorage/sessionStorage entry.

The CPAMP bridge supplies live CSS custom properties. Source-derived fallbacks
are in a lower-priority CSS layer, so they cannot override the injected host
tokens. See [`../DESIGN.md`](../DESIGN.md) for the source-backed visual contract.

## Data And Request Semantics

The server is authoritative for profiles, status, models, tier, usage, and token
validity. No account count, plan entitlement, quota, or successful registration
is manufactured. Expired/error/disabled accounts do not advertise available
models. Missing usage is visibly unknown, never unlimited or zero.

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
commit, push, independent agent review, or Lighthouse score is claimed here.
