# ChatGPT2API Manager Resource

A thin, read-only resource inside the existing Manager iframe. No application
shell, account/API-key editor, generation controls, or alternative dashboard.

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
Contract tests were run against a failing stub before implementation.

`qa` starts ephemeral loopback-only HTTP servers and isolated real Chrome
contexts, blocks off-origin requests, exercises the built artifact in a
source-matched mock host, and closes all contexts and servers in `finally`.
It checks initial/manual loading, updated counters, focus preservation, 401/403,
upstream-down HTTP 200, malformed data, missing/null account observations,
host auth errors, parent navigation, pagehide cancellation followed by a fresh
manual request, no polling/mutations/redirects, live host theme changes, reduced
motion, and long/HTML-like model names rendered as text.

Evidence is under ignored `evidence/`; `qa-report.json` enumerates scenarios
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

## Runtime Contract

- Public static resource: `GET /v0/resource/plugins/chatgpt2api/index`.
- Authenticated data: `GET /v0/management/plugins/chatgpt2api/status`.
- Only navigation: `/management.html#/ai-providers`, `target="_top"`, no query.
- One initial GET, then manual refresh only; no retries, intervals, redirects,
  account changes, enablement toggles, or generation requests.
- Required plugin/provider/mode/credential identities are validated. Model
  chips and counts come solely from `routing.model_names`; route_count is
  labeled as routing selections, never successful inference requests.
- Only present upstream version/active/total account observations are rendered.
  Null and missing values are not fabricated as zero. Limited/abnormal counts
  are parsed but do not add unrequested dashboard widgets.
- Unknown response fields, including raw `upstream.error`, are discarded.
  `healthy: false` selects fixed local failure copy; outer HTTP errors never
  expose their bodies. Failed refresh removes the previous successful snapshot.

## Reused Host Contracts

`src/host-auth.ts` is the small tested adapter from
`../../gemini-web-plugin/web/src/auth.ts`, with its import placed first.
The storage and transport semantics are unchanged. This repository's MIT
license covers the reused code; no third-party application source is vendored.

Only the same-origin parent's existing `cli-proxy-auth` Zustand envelope is
read. Plaintext and `enc::v1::` base64/XOR secureStorage formats are supported,
using the exact public host/UA-derived salt. Only `state.managementKey` and
same-origin `state.apiBase` are consumed. The resource never writes storage,
guesses global key aliases, reads React internals, or accepts URL credentials.
Full Manager mode uses the Manager admin context; the core adapter uses the
same supported storage envelope. Manager retains control of authentication.
Absent saved login or 401/403 directs users back to Manager, not a second key
form. The genuine ChatGPT Web account pool stays in the existing service.

`build.ts` reads the existing Gemini `tokens.css`, and the UI imports only its
small typed DOM primitives. The repository sibling must therefore be present
at build time, but not at runtime. Host-injected CSS tokens override fallback
tokens via the same low-priority CSS layer. See `../DESIGN.md` for the preserved
visual contract and explicit task boundaries.

## Diagnostics And Boundaries

Strict `tsc --noEmit`, Node checks, and real-browser scenarios are the executable
gates. The shared LSP intermittently times out on fresh TypeScript diagnostics;
the final directory scan covered all 13 TypeScript files with zero errors.
Applying
the generic Biome LSP to generated `index.html` reports minifier/library style
rules such as assignments in expressions. The artifact is not hand-edited and
no lint rule is disabled; its JavaScript is checked by V8 and real Chrome.
The optional shared skill checker requires TypeScript 7's unstable API, which
the repository's pinned TypeScript 5.9.3 does not expose. It was not used as a
passing gate and the repository toolchain was not upgraded to accommodate it.

Only `web/**` and the plugin `DESIGN.md` are owned here. Backend Go, plugin-root
README, core, ingress/CORS/Cloudflare config, real credentials, production
browser profiles, SSH, and deployment are untouched. The integrating owner
performs actual public-host Manager/CPA QA. No Lighthouse or independent
review-panel result is claimed for this explicitly bounded task.
