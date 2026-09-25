# GeminiWeb Manager Integration Build

This is a local, reviewable build recipe, not a deployment receipt. The running
Manager image, Compose, environment, keys and services are unchanged.
Current local checks and artifact hashes are recorded in [PATCH-EVIDENCE.md](PATCH-EVIDENCE.md).

## Pinned Input

- Repository: `https://github.com/seakee/CPA-Manager-Plus.git`
- Revision: `e1a8788ab796f4d001c5d1e9851c418989b05424` (`v1.12.11`)
- Patch: `patches/0001-gemini-web-manager.patch`
- Build version: `v1.12.11-geminiweb.1`
- Use Node 22.22.0 and the vendor's committed `package-lock.json` with `npm ci`.
- Keep the vendor MIT `LICENSE`; the script includes it with `management.html`.

## Build And Test

Choose a path that does not already exist:

```sh
bash deploy/cpa-manager-plus/build-patched.sh /tmp/opencode/manager-gemini-review
```

The script fetches the exact revision into a new vendor checkout, checks and
applies the patch, installs locked dependencies, runs typecheck separately,
runs the complete web test suite, builds the normal production single-file UI, and checks
demo isolation. It leaves `artifacts/management.html`, `artifacts/LICENSE`,
the exact combined `artifacts/0001-gemini-web-manager.patch`, `artifacts/SOURCE`,
`artifacts/SHA256SUMS` and the patched vendor build context for inspection.
It does not stage, commit, push, install, deploy, or restart anything.

For manual patch application, use only a fresh checkout of that exact revision:

```sh
GIT_MASTER=1 git -C /absolute/fresh/vendor apply --check /absolute/cliproxy/deploy/cpa-manager-plus/patches/0001-gemini-web-manager.patch
GIT_MASTER=1 git -C /absolute/fresh/vendor apply /absolute/cliproxy/deploy/cpa-manager-plus/patches/0001-gemini-web-manager.patch
```

## Manager Image Integration

The existing vendor `Dockerfile.manager-server` already compiles the UI and
embeds `dist/index.html` as `internal/httpapi/web/management.html` in the Manager
binary. Reuse it; a second Dockerfile or a runtime HTML override is unnecessary.
After reviewing the patch and artifact, an image can be built locally with:

```sh
docker build --platform linux/amd64 \
  --file /tmp/opencode/manager-gemini-review/vendor/Dockerfile.manager-server \
  --build-arg VERSION=v1.12.11-geminiweb.1 \
  --build-arg SOURCE_COMMIT=e1a8788ab796f4d001c5d1e9851c418989b05424 \
  --tag cpa-manager-plus:geminiweb-review \
  /tmp/opencode/manager-gemini-review/vendor
```

This command builds only. Upstream base-image tags are not content-addressed;
pin approved base-image digests separately if byte-reproducible OCI images are
required. No OCI image reproducibility or production verification is claimed.
The existing deployment still references its original image until separately
reviewed. Do not edit Compose or activate the image as part of this recipe.

## Behavior And Deliberate Limits

The original GeminiWeb Login button is now **Continue in GeminiWeb plugin**.
It checks backend plugin support, global enablement, the registered/enabled
GeminiWeb first resource, and the resource HTML response, then navigates to
`/#/plugin-pages/gemini-web/0` (under `/management.html` when hosted there).
It returns before creating native OAuth attempts, listing credential mutation
baselines, calling `/gemini-web-auth-url`, or starting a native poll. That
backend auth endpoint is still intentionally unsupported. The plugin owns
Google companion login and persistence. Other providers keep native OAuth.

For an existing `gemini-web` credential, open its native Quota tab using the
quota region or **GeminiWeb quota details** row action. **Load GeminiWeb quota**
uses `GET /v0/management/plugins/gemini-web/accounts`; subsequent refreshes use
`POST /v0/management/plugins/gemini-web/refresh` with only `{id}`. Both use the
existing Manager auth transport. There is no automatic load, quota poll,
second key prompt, credential mutation, or implicit enablement.

The manual adapter follows the existing quota configuration lifecycle but
deliberately bypasses Codex decoders, inferred windows, normalized percentages,
global plan counters, and window request estimates. The native detail section
shows provider tier/code, enabled/state observation, models, observation time,
remaining provider compute units, raw usage fraction, explicitly supplied
percentage, and reset time. Missing values remain Unknown. Windows appear only
when supplied, including `ai_credit` and `unknown`. A failed refresh retains the
previous observation only with an explicit stale notice. An HTTP 200 body with
`status: error` or `expired` is not success. Missing/null metrics are not zero
or unlimited quota. Opaque tier codes are never renamed to PRO.

The list's aggregate quota and plan counters intentionally remain unknown for
GeminiWeb. Batch quota refresh reports guidance to the manual detail section,
not false success. Request-level all-provider usage is unchanged and remains in
the existing summary above the plugin measurements. Detailed login/registration
remains accessible through the checked **Open GeminiWeb plugin panel** action.

## Isolated Browser QA

Use a locally installed Playwright/Playwright Core ES module and Chromium.
For a separate disposable QA tool installation, install `playwright@1.58.2`
with npm in a new directory outside the vendor checkout. No production browser
profile, real credential, Google call or 1Password call is needed.

```sh
node deploy/cpa-manager-plus/tests/gemini-web-browser.mjs \
  /tmp/opencode/manager-gemini-review/vendor \
  /tmp/opencode/manager-gemini-review/browser-evidence \
  /absolute/qa-tools/node_modules/playwright/index.mjs
```

If needed, `CHROMIUM_PATH` selects an already-installed Chromium executable for
this test process only. The harness launches a fresh browser, binds a fixture
server to a random loopback port, blocks non-fixture browser requests, serves
the actual production HTML, and logs only synthetic request paths/bodies.
It exercises the real Login button and native account detail surface at
1280/768/375px, plus missing-resource, nullable quota and refresh-failure paths.
Screenshots and `browser.json` are written to the chosen evidence directory.
The browser and fixture server are closed even on failure. The embedded plugin
portal is a clearly labeled HTML fixture; this does not verify Google login.

## In-Memory Plugin Requests

The same patch now installs `__CPAMP_PLUGIN_HOST__` only on the owned, registered,
enabled Gemini Web or ChatGPT2API resource iframe. It is a frozen, non-enumerable
capability with version, plugin ID, exact resource URL, and a request function.
It contains no credential. The child validates those fields and prefers this
transport over the existing explicit remembered-login/standalone adapter.
Remember off remains off; no key is persisted, copied to the child/companion,
sent in window messages, or requested a second time.

The parent uses the current native scoped API client internally. Each call checks
the connected auth, credential/target identity, original Document, iframe owner,
exact registered resource URL and same origin. Input is limited to root-relative
`/v0/management/plugins/<that-plugin>/` paths: Gemini GET/POST, ChatGPT GET.
ChatGPT additionally permits only the exact POST paths `import-codex`,
`refresh-web`, and `set-web-enabled`. Bodies must be JSON objects no larger than
4096 UTF-8 bytes. Import accepts one `codex_<64 lowercase hex>` ID, literal
`consent:true`, and optional literal `allow_disabled_source:true`; refresh accepts
only one `web_<64 lowercase hex>` ID; toggle accepts that Web ID, boolean
`enabled`, and literal `consent:true`. Unknown keys, credential fields, arbitrary
ChatGPT POSTs, generic management and cross-plugin requests stay denied. The
backend independently checks consent, the selected source/target and vendor
capability. Gemini's POST and observed-account registry policies are unchanged.
Caller headers, alternate origins, URL queries/fragments in plugin paths,
traversal, encoded escapes and extra options are rejected before transport.
The native fetch adapter refuses redirects and adds no timeout or retry.
Only `{status,body}` crosses back; response headers and Axios request/config/error
objects never do. Unknown failures are fixed safe errors. Four existing fixed
Gemini error codes remain whitelisted so a rejected Google session does not log
out an otherwise authenticated Manager; actual Manager 401 still logs out.
Only documented ChatGPT account error codes are admitted in the ChatGPT scope,
including fixed HTTP 200 body errors. Error responses are reduced to `{error}`;
unknown error text/configuration never crosses the bridge. A ChatGPT account
error is not a Manager authentication failure. No auth middleware or credential
transport has been replaced by this extension.

Requests abort and capabilities revoke on auth/target change, navigation,
reload and unmount. Native Navigation events include pushState/replaceState;
the bridge fails closed in browsers without that API (no History monkeypatch,
poller or weaker fallback capability). Current Chrome is the verified browser.
The keyless ready event permits one initial missing-context retry per child
document. Repeated ready/style signals cannot initiate query loops. A grouped
account/model read retains its chosen transport, never a copied credential,
so a late bridge install cannot split a Remember-on legacy read midway.

Gemini also needs host registry proof before advertising usable models. The only
extra bridge endpoint is `GET /v0/management/auth-files/models?name=<id>`, with
exactly that query and a `gemini-web-[A-Za-z0-9_-]+.json` name observed in this
document's authenticated Gemini account-list or refresh response. ChatGPT,
unknown accounts, cross-provider names and writes are denied. Only public model
metadata is returned. Empty/failed registration leaves models unconfirmed; one
explicit Check Status can re-read, with no polling or generation. Native manual
quota and the original Login-to-portal branch remain unchanged.

These are trusted same-origin plugin documents, not hostile JavaScript sandboxes:
same-origin scripts already share browser origin privileges. The capability
does not claim to isolate arbitrary same-origin XSS from the parent.

## Actual Nested Build QA

Build both separately owned plugin artifacts first with their documented Bun
typecheck/build/test/qa commands. Then run the actual built Manager containing
the actual built Gemini/ChatGPT resource HTML, not a placeholder child:

```sh
node deploy/cpa-manager-plus/tests/plugin-request-browser.mjs \
  /tmp/opencode/manager-gemini-review/vendor \
  /tmp/opencode/manager-gemini-review/nested-browser \
  "$PWD/deploy/gemini-web-plugin/web/node_modules/@playwright/test/index.mjs"
```

An optional final argument filters scenario names for focused reproduction.
Every scenario has its own loopback fixture server and fresh Chrome context;
only synthetic keys/API responses are used. The fake companion is installed at
the external-port boundary after the real child loads. The harness checks desktop,
tablet and mobile light/dark, real UI login with Remember off/on, provider quota
and status, empty/failed registry, exact Origin, scope denial, secret-free results,
redirect rejection, logout/reload/history/unmount aborts, fresh-key login, and
legacy standalone behavior. `browser.json` records paths, safe synthetic outcomes,
PNG hashes and cleanup. No production browser, real Google login, extension
installation, deployment or secret-manager access is performed.

The GPTWeb account surface has a separate harness in its owned plugin tree:

```sh
node deploy/chatgpt2api-plugin/web/tests/accounts-browser.mjs \
  /tmp/opencode/manager-gemini-review/vendor \
  /tmp/opencode/manager-gemini-review/gptweb-browser \
  "$PWD/deploy/chatgpt2api-plugin/web/node_modules/@playwright/test/index.mjs"
```

Choose a new evidence directory. This keeps the original 23 nested scenarios
intact and adds 20 account/import/quota/toggle scenarios using synthetic scoped
API DTOs. It checks Remember off, exact ID/boolean POST bodies, no iframe key or
direct authenticated iframe requests, disabled source consent, disabled imports,
duplicate preservation, capability/configuration errors, stale quota/rotation,
explicit Web-only toggles and keyboard states at all three light/dark widths.
See the [resource UI guide](../chatgpt2api-plugin/web/README.md) for data semantics.
