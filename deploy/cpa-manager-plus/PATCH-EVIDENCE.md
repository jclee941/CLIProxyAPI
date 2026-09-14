# GeminiWeb Manager Patch Verification

Local verification on 2026-09-13. No deployment or production account calls.
The native-quota receipt below is retained as the earlier baseline. The current
combined patch and Remember-off bridge receipt are in **In-Memory Bridge Follow-up**.
The latest combined artifact and account UI receipt are in **GPTWeb Account UI
And Narrow POST Extension**; the earlier receipts remain historical baselines.

## Inputs And Artifacts

| Item | Value |
| --- | --- |
| Vendor commit | `e1a8788ab796f4d001c5d1e9851c418989b05424` |
| Vendor tag / license | `v1.12.11` / MIT, unchanged |
| Patch SHA-256 | `987e37da9784845bfc83495f1f9b9a561b9eb98af55ccb5ef7f45fbff0ffa1e9` |
| HTML SHA-256 | `cd98ce1e1e1c136af4fe3e351454bbe0b273e70db4d323cf38c9b5951662a50c` |
| LICENSE SHA-256 | `4c34f41a29f20a622375ead744944e1d0d1a148f9677fae65b2601eb0949bc7f` |
| Toolchain | Node `22.22.0`, npm `10.9.4`, locked Vite `8.0.10` |
| Final clean build | `/tmp/opencode/manager-gemini-final-f6604e63/vendor` |
| Reviewable output | `/tmp/opencode/manager-gemini-final-f6604e63/artifacts/management.html` |
| Build log | `/tmp/opencode/manager-gemini-f6604e63/final-build.log` |
| Browser receipts | `/tmp/opencode/manager-gemini-final-f6604e63/browser-evidence/browser.json` |

The working implementation was in the separate
`/tmp/opencode/manager-gemini-f6604e63` clone. The pre-existing vendor directory
was read only. The maintained build script fetched the pinned source afresh,
applied the maintained patch, and reproduced the same HTML hash in two clean
builds. Only this package supplies the patch; no dirty root Go code is built.

## RED And GREEN

- Original OAuthPage button regression: 5 expected failures, 1 native Codex
  control passed. GeminiWeb entered native start instead of navigation, and
  unavailable resources did not produce the required error.
- New account API and adapter tests initially failed because those adapters
  did not exist. The API tests are boundary/transport unit tests; actual HTTP
  integration is covered by the production-HTML browser fixture.
- Native account row action: the browser initially failed to open the quota
  section because generic refresh returned an unsupported-provider error.
- Final clean build: **416 tests passed in 10 files**, including all 279
  existing AccountsPage tests. Other built-in OAuth providers and GPTWeb's
  native start path are covered without modifying those integrations.
- `npm run type-check` passed separately from `npm run build`.
- Production single-file build and `check:demo-isolation` passed.
- ESLint passed on all changed vendor source/test files. `git diff --check`,
  `bash -n build-patched.sh`, and `node --check tests/gemini-web-browser.mjs`
  passed. The builder refused missing arguments and an existing checkout.

## Real Browser Surface

The harness served the built HTML on an ephemeral loopback port and launched
a fresh isolated Playwright Chromium browser. The fixture rejects missing or
wrong Manager authorization on management requests and blocks external browser
requests. All credentials and account data are deliberately synthetic.

At **1280, 768, and 375px**, the real Login button reached the plugin route
before any GeminiWeb native auth or status request. Codex still started and
polled. A resource HTTP 503 left an actionable error without native fallback.
The original native account row action opened GeminiWeb quota details.

Each viewport recorded exactly **one explicit GET accounts and three explicit
POST refresh requests**, every refresh body exactly `{id}`. There were no
GeminiWeb quota requests on mount. Actual units, raw fraction, opaque tier code,
unknown tier/percentage/reset and dynamic windows were rendered. HTTP 200 with
`expired` plus null usage, and HTTP 503, both retained old evidence with a stale
notice; a later ready/null response cleared the stale measurement. No fabricated
percentage bar, request count, PRO tier, 5-hour window or weekly window appeared.

There are **27 PNG captures**: `oauth`, `portal`, `portal-error`, `quota-idle`,
`quota-measured`, `quota-metrics`, `quota-body-error`, `quota-http-error`, and
`quota-null`, each at the three widths. `browser.json` records each PNG's
dimensions/hash, zero page errors and zero document overflow for all runs,
request paths/bodies, and browser/server cleanup receipts.

Two independent read-only reviews returned **PASS**: source/functional integrity
and visual inspection of all **27/27** final captures. Neither reported blocking
findings. Full hover/loading-state visual coverage was not claimed; the native
Button focus state was exercised and existing component behavior was preserved.

## Limits And Preserved State

- All changed TypeScript source/tests have clean error diagnostics; the OAuth
  renderer test inherits upstream `react-test-renderer` deprecation hints.
  Markdown and shell diagnostics were clean. No LSP is configured for `.patch`;
  patch application is verified by clean replay. Fresh diagnostics for the
  package's `.mjs` browser harness timed out; Node syntax and full execution pass.
- This is not a Lighthouse/performance certification, an OCI image build, a
  Google login test, or production quota verification. No dependency upgrade,
  global style change or unrelated warning fix was made.
- Native aggregate quota/plan counters intentionally remain unknown for
  GeminiWeb; actual values are in the manual native Quota detail section.
  The backend native GeminiWeb OAuth endpoint remains unsupported.
- Root staged paths remained **13**, with staged binary-diff SHA-256 unchanged:
  `6daeee61a55052ec38662c1dc600456ec9ad38723cd078729f91273cedc52ae9`.
  No staging, commit, push, Compose/environment/key edit, restart, or deployment.

## In-Memory Bridge Follow-up

Current combined patch, same pinned vendor commit, with all **12** earlier native
quota/OAuth patch files verified byte-identical to the earlier read-only replay.
Only the registered resource page and its scoped request module/tests were added
to that vendor patch. Plugin adapters, registry guard and their generated HTML
remain in the two separately owned `web/**` trees.

| Artifact | SHA-256 |
| --- | --- |
| Maintained `0001-gemini-web-manager.patch` | `88f27dfdbdd66902a194566daf3eaada8d12bc6a7f1765e3d959c773f728e112` |
| Clean replay `artifacts/management.html` | `88099d8ba5ac01b584e361db68c4006a70d5edfc5db94cc0c595e7287481b975` |
| Gemini `web/index.html` | `9af675d1538b54a9da8d7fe2f718c1ce1b0429de773e752e11a0ab1b731ace5c` |
| ChatGPT `web/index.html` | `2edeedab4df5d814cdfc691c3b49d080f4a925299a5d167b41c9ef5aafc67d3a` |

- Owned working checkout: `/tmp/opencode/manager-bridge-f657e90b/vendor`.
- Original input patch retained at `/tmp/opencode/manager-bridge-f657e90b/input-manager.patch`.
- Fresh pin/patch replay: `/tmp/opencode/manager-bridge-replay-f657e90b/vendor`.
- Final actual nested browser receipt: `/tmp/opencode/manager-bridge-replay-f657e90b/nested-browser/browser.json`.
- Original native surface rerun: `/tmp/opencode/manager-bridge-replay-f657e90b/native-browser/browser.json`.
- No foreign checkout was written, staged, committed or reset. Both owned checkouts
  and evidence are retained intentionally for review; their servers and browsers
  are closed. No production browser/CDP, secret manager or Google connection was used.

### Red/Green Evidence

`red-browser/failure.png` under the working directory shows the actual built
Manager logged in with Remember off and the actual built Gemini child displaying
the existing missing-Manager-auth state. `/config` and `/plugins` succeeded, but
there was no plugin `/accounts` request. This reproduces the reported transport
break without touching the live surface.

`red-registry-boundary` then failed because private backend `ready/models_ready`
produced a success badge while the host registry was empty. `red-history` proved
that pushState did not abort an active request before the Navigation event guard.
`red-google-401` proved the native 401 interceptor incorrectly logged out Manager
when the plugin rejected a Google session. Each now passes in the final suite.

Remember-on also exposed a transport-selection race: a legacy account GET begun
before iframe onLoad could finish after the bridge appeared, causing its registry
read to use a capability that had not observed that account. Grouped account/model
reads now retain the selected transport, not a credential copy; later requests
prefer the fresh bridge. The retained legacy and standalone tests pass.

### Final Gates

- Clean patch apply to the pin, locked `npm ci`, separate typecheck, full **3,331
  tests / 225 files**, production build and demo-isolation check: passed.
- Scoped Manager ESLint, shell syntax, Node syntax and root `git diff --check`: passed.
- Gemini strict typecheck/build, **58 unit/artifact tests**, **58 browser scenarios**
  and 46 screenshots: passed. Final plugin evidence: `web/evidence/companion-4hpvQO/`.
- ChatGPT strict typecheck/build, **50 unit/artifact tests**, **24 browser scenarios**:
  passed. Its original mocked-host suite remains separate from actual nested QA.
- **23 actual nested build scenarios**: passed. Real Manager UI login, untouched
  Remember-off checkbox, authenticated Gemini account/quota display and ChatGPT
  status; Remember-on/standalone; fixed safe errors; redirect/header/method/path,
  encoded traversal and cross-plugin denial; unowned iframe denial; exact Origin;
  one ready retry/no loops; logout/reload/document/history/unmount network abort;
  old capability denial and fresh-key re-login; empty/failed registry followed by
  one explicit successful recheck; Google 401 versus Manager auth semantics.
- **17 nested screenshots** cover both plugins at 1280/768/375, light/dark, plus
  pending/ready registration and stale quota. The original native suite also passed
  at all three widths with **27 screenshots**, no native Gemini login request,
  exactly four manual quota calls per width, no overflow and no page errors.
- Final independent read-only correctness and visual reviews returned **PASS**
  without blockers. The visual review opened all **17/17** fresh replay captures;
  the correctness review rechecked the fixed History-navigation abort and Google
  session 401 boundaries against the final source and 23-case receipt.
- Final LSP directory scans: Gemini source **13 files**, tests **10**; ChatGPT source
  **6**, tests **8**; Manager plugin TypeScript **14**, plus changed resource TSX;
  Manager browser harness **4 MJS**: zero error diagnostics. The untouched Gemini
  login API has one async-conversion hint. Individual fresh LSP calls intermittently
  timed out, so directory scans were used. Markdown/shell diagnostics passed.
- Generated plugin HTML retains the already documented Biome minifier/library
  style diagnostics. It is not hand-edited or suppressed; V8 artifact checks and
  real Chrome execution pass. No `.patch` language server is configured; replay
  and full build/test are its validation gate.
- The staged path count remains **13**, and the staged binary-diff fingerprint is
  still `6daeee61a55052ec38662c1dc600456ec9ad38723cd078729f91273cedc52ae9`.

The bridge requires native Navigation events and fails closed where unavailable.
It scopes requests from trusted registered same-origin documents; it is not a
sandbox against arbitrary same-origin XSS. No new CSS, model generation, provider
poller, automatic Remember setting, key persistence or extension transport exists.
This receipt makes no production readiness, deployment, real-account quota,
Google login, or Lighthouse certification claim. Reproduction commands are in
[PATCH-BUILD.md](PATCH-BUILD.md#actual-nested-build-qa).

## GPTWeb Account UI And Narrow POST Extension

Local synthetic-only verification on 2026-09-13. The vendor pin and build version
remain unchanged. This increment adds the existing GPT resource's source/import,
independent Web quota and explicit target toggle UI plus a narrow Manager grant.
No backend, source credential, Gemini implementation, runtime configuration,
production image or foreign worktree was written by this UI task.

| Artifact | SHA-256 |
| --- | --- |
| Combined `0001-gemini-web-manager.patch` | `be17b711bc4758d47a6c2222e122ba171822c876c24c1675ffba142073e4dcc0` |
| Clean replay `artifacts/management.html` | `7da267c8a81fdf4cddac05e118598aa9eeb957de2a6764f93137e386bc082cee` |
| GPT resource `web/index.html` | `1f3bda53d85f3edd70472aa0a6059d09ff9b144d25eb90973d718bd4dd073c9b` |
| Unchanged Gemini resource `web/index.html` | `9af675d1538b54a9da8d7fe2f718c1ce1b0429de773e752e11a0ab1b731ace5c` |

### Provenance And Scope

- Owned working copy: `/tmp/opencode/manager-gptweb-f651ba3d`.
- Input patch retained there as `input-manager.patch`, hash `88f27dfdbdd66902a194566daf3eaada8d12bc6a7f1765e3d959c773f728e112`.
- `preserved-baseline.json` verifies **14 original patch files byte-identical**,
  including all 12 native Gemini/OAuth files, the resource page and original
  bridge tests. Only `pluginHostRequest.ts` changed; `chatgptHostPolicy.ts` and
  its 32-case test file were added. The existing Gemini POST and registry rules,
  document/origin/current-auth/revocation guards and memory-only bridge remain.
- Maintained script fetched the pin afresh into
  `/tmp/opencode/manager-gptweb-replay-f651ba3d/vendor` and replayed the combined
  patch. `artifacts/management.html` matches the working build byte-for-byte.
  The builder now includes the exact patch plus `SOURCE` in artifact checksums.
- Build log: `/tmp/opencode/manager-gptweb-f651ba3d/replay-build.log`.
- Root staged binary-diff fingerprint remains
  `6daeee61a55052ec38662c1dc600456ec9ad38723cd078729f91273cedc52ae9`.
  No root staging, commit, push, image build, restart or deployment occurred.

### Tests And Actual Surface

- RED: the existing built GPT iframe lacked `#account-area`; its actual Manager
  Remember-off reproduction is in `manager-gptweb-f651ba3d/red-ui/`. The broker
  tests initially had **6 expected failures** for the three new POST contracts
  and fixed ChatGPT error scope. Source/consent contract tests preceded their
  implementations.
- Fresh replay: locked dependency install, separate TypeScript check, all
  **3,363 tests in 226 files**, production build and demo-isolation check passed.
  Scoped Manager ESLint and shell/Node syntax checks passed.
- GPT strict typecheck/build and **75 unit/artifact tests** passed. Its original
  **24 status-browser scenarios / 24 captures** remain intact, with the new
  list-reload control included in keyboard navigation. Runs use unique
  `web/evidence/status-*/` paths.
- Final actual nested GPT receipt:
  `/tmp/opencode/manager-gptweb-replay-f651ba3d/gptweb-browser/browser.json`.
  **20 scenarios / 37 PNGs** passed, serving the actual built Manager and GPT
  iframe over a loopback mock HTTP boundary. The six light/dark/width combinations
  each cover `sources`, `import-consent`, `enable-consent`, and `quota` at
  375/768/1280px. Edge captures cover disabled-source approval, duplicates,
  unsupported/unknown/unconfigured/malformed states, stale quota, rotation,
  wrong identity, pending toggle and safe account-auth failure.
- Exact mutations in each main flow: one consented disabled import, one explicit
  Web enable, one selected Web quota refresh, one explicit Web disable. No automatic
  activation, background quota poll, bulk operation or Codex source change occurred.
  Bodies contain only opaque IDs and booleans. Child DOM/globals/logs/decoded
  localStorage and sessionStorage contain no Manager key; authenticated account
  network requests originate in the parent. HTTP 200 body errors remain failures.
- Original **23 nested scenarios** passed unchanged against the clean replay:
  `/tmp/opencode/manager-gptweb-replay-f651ba3d/nested-compatibility/browser.json`.
- Original native Gemini/OAuth regression passed at all three widths, with
  **27 captures**, zero native Gemini login requests, Codex start/poll preserved,
  four explicit quota calls per width, no overflow or page errors:
  `/tmp/opencode/manager-gptweb-f651ba3d/native-compatibility-chrome/browser.json`.
- Gemini source, build inputs and tests were copied to the owned
  `/tmp/opencode/manager-gptweb-f651ba3d/gemini-compatibility`, not edited in place.
  Strict typecheck/build, **58 tests**, **58 browser scenarios / 46 captures**
  passed. Built HTML matches the original Gemini resource hash. Receipt:
  `gemini-compatibility/evidence/companion-n2FXvq/qa-report.json` under that copy.
- One independent read-only gate reviewer returned **APPROVE**, with no blocking
  code/security/interaction or visual findings, directly opening **37/37** GPT
  captures in `manager-gptweb-f651ba3d/green-ui-3/`. The final replay uses the same
  byte-identical Manager and GPT artifacts. Review task: `bg_50e35546`.

### Diagnostics And Unverified Gates

Authored GPT source (12 TS files), tests (12 TS files), Manager plugin modules
(16 TS files), changed MJS harnesses, CSS, Markdown and shell diagnostics are
clean. Individual LSP requests intermittently timed out; directory scans were
used. Generated GPT HTML still triggers the documented Biome minifier/library
style diagnostics; it is neither hand-edited nor suppressed, and V8/Chrome pass.
No patch language server is configured; clean replay is the patch gate. One
native harness invocation needed its documented `CHROMIUM_PATH` override because
the optional headless-shell executable was absent; installed Chrome then passed.
The replay build emitted a non-failing Vite plugin-timing advisory.

Context7 was quota-limited; Zod 4.6's official API documentation and the pinned
installed implementation/tests supplied the schema reference instead. No toolchain
upgrade, secret access, production browser or real upstream call was used.
Servers, isolated Chrome contexts and browsers were closed after each run.
Owned checkouts and evidence are intentionally retained for inspection.

Live scoped host callbacks, host bootstrap `CHATGPT2API_AUTH_KEY` injection,
homogeneous patched vendor rollout, real access-snapshot import/rotation and Web
entitlements remain unverified operator gates. This is not a production readiness,
Lighthouse, live-login, OCI-image or deployment receipt.
