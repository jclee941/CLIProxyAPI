# Gemini Web Login Companion

An installable, vendor-derived Manifest V3 **login-only** companion for Chrome
132+. It transfers one explicitly selected Gemini tab's session to one original
portal runtime port after user consent. It never talks to Manager HTTP APIs,
collects a management key or Google password, generates model responses, exports
files, copies to the clipboard, or stores tokens. Flash/Omni remain direct HTTP
outside this extension. No CDP is used by the installed extension.

Only this directory is owned by this deliverable. Plugin Go, `web/`, parent
design, Manager, and deployment are unchanged. See [DESIGN.md](DESIGN.md) and
[PROVENANCE.md](PROVENANCE.md); the pinned vendor MIT license is packaged verbatim.

## Install The Production Build

From this directory, with Bun 1.3+ and the local `zip` / `unzip` utilities:

```sh
bun --no-env-file install --frozen-lockfile --ignore-scripts
bun --no-env-file run check
bun --no-env-file test
bun --no-env-file run build
```

The production ZIP is `artifacts/gemini-web-login-companion-0.2.0.zip`.
Its root contains `manifest.json`, worker, popup assets, and license/docs.
The equivalent unpacked directory is **`dist/production/`**.
The bundled Zod runtime's MIT notice is also included as `LICENSE.zod`.

1. Extract the production ZIP to a permanent directory, or keep `dist/production`
   at its current absolute path. Never install the `LOCAL-QA` ZIP for real login.
2. Open `chrome://extensions`, enable Developer mode, choose **Load unpacked**,
   and select the directory containing `manifest.json`.
3. Grant the complete declared Google site access (`https://*.google.com/*`),
   including the parent-domain cookies, and the declared portal host. Limiting
   access to only `gemini.google.com` can hide parent cookies and is rejected.
   The companion provides an explicit permission-grant button if access is withheld.
4. Record the installed 32-letter extension ID from Chrome or the extension's
   **extension ID** disclosure. Configure that ID in the portal's companion-ID
   setting. The operator owns this value: **there is no universal hardcoded ID**.
5. Start login in the production Gemini Web portal. In the separate extension
   window, review the destination, explicitly select one tab and account index,
   check consent, then approve. With no eligible tab, open Google Gemini, complete
   login/2FA yourself, return to this window and refresh the tab list.
6. The original portal receives the session and performs its own HTTPS completion.
   Its `ack` confirms receipt, not necessarily backend persistence. Check the
   final account state in the portal. Closing either side cancels further capture.

Unpacked IDs depend on the installation path. Keep the folder stable between
updates and use Chrome's Reload button. If the folder moves or Chrome assigns a
different ID, update the portal setting; never guess the ID or add an origin
fallback. No private key is generated, required, or committed. Incognito is
explicitly unsupported; install in the normal profile containing the desired
Google login. No other physical Chrome profile is opened or inspected.

## Portal Port Contract

Production authority is exactly `https://cliproxy.jclee.me` and pathname
`/v0/resource/plugins/gemini-web/index`. The manifest has no external extension
IDs, wildcard portal origins, content scripts or web-accessible resources.
The worker independently checks `port.name`, `sender.url`, `sender.origin`,
tab/frame/document identity and active lifecycle. It checks the original document
URL again before capture and before delivery, including same-document history
changes. Resource iframes are supported without trusting their parent page URL.

```typescript
const port = chrome.runtime.connect(operatorConfiguredExtensionId, {
  name: "gemini-web-login",
});
port.postMessage({ type: "begin", state, expected_identity });
```

Omit `expected_identity` for new accounts; for replacement it must be exactly
`{account_sha256: <64 lowercase hex>, auth_user: <nonnegative safe integer>}`.
The portal sends no tab selection or cookie/token value to the companion.
All messages are strict: unknown fields, wrong types/states and invalid ordering
close the connection. A state is exactly 64 lowercase hexadecimal characters.
There are at most 32 active connections, including uninitialized ports, with an
absolute 10-minute lifetime from connection (no sliding expiry).

The **only** external replies, on the original port, are:

```typescript
{ type: "ready", state }
{ type: "session", state, token, account_sha256, auth_user }
```

After receipt the portal sends `{type:"ack",state}`; at any time after `begin`
it may send `{type:"cancel",state}`. Errors use disconnect, not a third external
reply shape. Safe fixed error text is shown only in the extension window.
There is no `window.postMessage`, arbitrary `onMessageExternal`, HTTP completion,
management key, clipboard, download, polling or alternate transport.

The own-page consent port is bound to the extension-created tab, never a state in
a URL. It carries only tab IDs, account indexes and fixed status. An unchecked
radio group, independent checkbox and approval button prevent first-tab fallback.
Refreshing or opening Gemini clears selection; neither action reads cookies.
While that window is open, a 20-second internal keepalive supports the bounded
MV3 workflow. It never renews expiry or performs capture. Worker loss disconnects
the workflow; no token or consent is restored.

The only additional permission is `storage`: `chrome.storage.local` holds
bounded replay tombstones (SHA-256 of the nonce plus expiry), never the raw state,
tokens, cookies, account identity or tab metadata. This blocks replay across worker,
extension and browser restarts for the original TTL. The server must independently
enforce one-use login states; uninstalling an extension removes its local state.
The 4096-tombstone cap fails closed until entries
expire; expired entries are pruned on the next claim. Active request state, UI
references and listeners are cleared on ack/cancel/disconnect/expiry. JS strings
cannot be securely zeroized; this is reference removal, not a memory-erasure claim.

`cookies`, `tabs`, `scripting` support capture and explicit selection. The portal
host permission is used only for exact-document URL revalidation, not HTTP calls.
`permissions.contains` verifies the full grant before selecting and capturing,
and after capture. CSP has `connect-src 'none'`, no remote code or framed popup.

## Public Contract

```typescript
import { captureGeminiSession } from "./capture.ts";

const session = await captureGeminiSession(chrome, {
  tabId: explicitlySelectedTabId,
  expectedIdentity: {
    account_sha256: expectedAccountSHA256,
    auth_user: expectedAuthUser,
  },
});
```

`captureGeminiSession(browser: ChromeCaptureApi, selection: CaptureSelection)`
returns `Promise<CapturedSession>`. The narrow adapter accepts Chrome's actual
`cookies`, `tabs`, and `scripting` methods; its compatibility with `typeof chrome`
is checked by TypeScript. It never queries the active tab or chooses a fallback.
The caller must obtain explicit user approval before calling it.

`expectedIdentity` is optional for an initial capture with no existing binding.
When supplied, both its lowercase 64-digit SHA-256 digest and its nonnegative
safe-integer account index must match. The request is parsed into a private copy
before any asynchronous work, so later caller mutation cannot change selection.
No profile GUID, account name, email, label, native record ID, or portal state is
accepted or inferred.

The result has exactly these fields:

```typescript
type CapturedSession = {
  readonly token: string;
  readonly account_sha256: string;
  readonly auth_user: number;
};
```

`token` is `gemini-web:v1:` followed by canonical unpadded base64url of UTF-8 JSON
with exactly `{"cookie":"<full ASCII header>","auth_user":<integer>}` in that order.
The complete encoded token, including its prefix, must be at most 32768 bytes.
The payload is encoded with `TextEncoder`; Gaia is hashed with Web Crypto SHA-256.
This matches the existing Go `credentials.go` parser and `contract_test.go`
RawURLEncoding example without changing either protocol.

Encoding is not encryption. The token is returned only in memory. This module
does not log, display, download, copy, persist, or transmit it. The caller must
not stringify the result into diagnostics, put it in a URL, or retain it as UI
state. All failures are `CaptureError` instances with fixed `code`/`message`;
underlying API errors and their causes are discarded without string conversion.

## Isolation Rules

1. Only the explicitly selected, loaded, non-discarded, non-frozen tab is read.
   Its URL must be canonical HTTPS on exactly `gemini.google.com`, without
   credentials, explicit ports, encoded path segments or ambiguous account
   selectors. Benign query/fragment data, including `?hl=ko`, is preserved exactly.
   An absent selector means `auth_user: 0`.
   `/u/N` accepts canonical decimal nonnegative JavaScript safe integers only.
   A single canonical `authuser=N` query is accepted, and must agree with `/u/N`
   when present. Repeated, conflicting, encoded, case-variant or nonnumeric
   selectors and fragment account selectors are rejected before store/page reads.
   No user tab URL is stripped, normalized or navigated by capture. The companion
   also binds the selected URL and cookie-store ID across the consent interval.
2. `getAllCookieStores()` is used only for membership metadata. Exactly one store
   must contain the selected tab. Every cookie query specifies both that store
   and a required Gemini URL. There are no store-wide cookie reads, other-store
   reads, broad Google-domain queries, or all-partition queries.
3. A MAIN-world, top-frame-only script checks its own URL before reading just
   `WIZ_global_data.S06Grb`, `W3Yyqf`, and `qDCSke`. All three must be the same
   21-digit string. Missing fields, non-string identities, script failures,
   multiple results, missing document IDs, and non-main frames fail closed.
   No HTML, performance entries, XSRF/build values, or other WIZ fields are read.
4. Cookie reads are bracketed by independent tab, document ID, exact URL,
   account-index, store-membership, Gaia-digest, and partition checks. A changed,
   missing, or mismatched identity never returns a token. The post-read injection
   targets the current top frame, not a potentially detached old document.
5. The document's partition key is resolved with `cookies.getPartitionKey`
   using its tab, main frame, and document ID. Only an explicit first-party
   `https://google.com` key without cross-site ancestors is understood. Opaque,
   incomplete, or extended partition metadata is rejected. Applicable partitioned
   cookies are queried separately and rejected, not silently omitted. Partitions
   belonging to other top-level sites are not queried.
6. The selected URL, the sidecar's account-prefixed `/app`, `batchexecute`, and
   `StreamGenerate` URLs must all produce the same complete header. These routes
   come from [account.py](../../gemini-web2api/extension/account.py) and
   [native.py](../../gemini-web2api/extension/native.py). All applicable unpartitioned root-path cookies
   are included, including HttpOnly, persistent, session, unknown-name, and
   empty-value cookies. There is no required-cookie list or scoring. Duplicate
   names, any applicable non-root path, unsupported scope, invalid ASCII cookie
   grammar, or changing cookie sets fail closed rather than being dropped,
   merged, quoted, sanitized, or guessed. Oversized tokens stop after the first
   URL's cookie reads, before collecting additional data.

## Capture Verification

Run from this directory with Bun (verified with 1.3.14):

```sh
bun install --frozen-lockfile --ignore-scripts
bun run check
bun test
bun test --coverage
bun run build
```

The independent manifest and lockfile pin TypeScript 7.0.2, Chrome API types,
Bun types, and Zod. Nothing is installed globally or into the existing `web/`
package. `dist/`, `node_modules/`, and coverage output are ignored locally.

The 152 deterministic tests use only synthetic, unprivileged fixtures. Their
in-memory Chrome API fake implements store/domain/host-only/path/partition
filtering and has its own behavioral tests. The actual injected function is
serialized and executed in a fresh VM realm, matching Chrome's closure-loss
boundary. Traps reject HTML, performance, network, storage, console, or other
page-surface access. Distinct fixtures prove profile isolation and explicit tab
selection even when a different account is active in the same store.

Historical evidence from the initial capture-only implementation (current full
companion verification is described in the browser QA section below):

- Empty-package baseline: `bun test --pass-with-no-tests` reported zero test
  files; `bun run check` reported TS18003 (no input files). Neither was treated
  as implementation coverage.
- First RED: `bun test tests/capture.test.ts` reported 0 pass, 1 fail, 1 error
  because the new `capture.ts` API did not yet exist. Typecheck reported TS2307
  for that same missing module, not an unrelated dependency error.
- Additional RED: unsupported opaque partition metadata initially resolved
  instead of rejecting. A strict partition schema closed that boundary.
- Additional RED: oversized-token tests observed 8 cookie queries rather than
  the required 2. Moving the size gate immediately after the first header fixed
  unnecessary subsequent reads.
- GREEN: `bun run check` exited 0; `bun test` passed all 152 tests, 0 failures.
  `bun test --rerun-each 3` also passed all 456 executions, 0 failures.
  Coverage measured 100% functions/lines for `capture.ts`. The injected function
  executes in the VM realm, so adapter source-line coverage is not a complete
  measure of its execution; no whole-module 100% coverage claim is made.
- `bun run build` produced a browser-target ESM bundle. A separate Bun driver
  imported that built bundle and exercised successful isolated capture and
  changed-Gaia rejection through the fake, with zero forbidden page reads.
- LSP was requested for every changed source/configuration file. Completed
  checks reported no errors; Bun promise matchers produced non-error `await`
  hints in tests. Fresh `capture.ts` diagnostics and the final fake-file check
  exceeded the tool's 3-second wait despite working document-symbol lookup and
  passing full `tsc --noEmit`. `.lock`, `.vendor`, and `.gitignore` have no
  configured LSP. Full fresh-LSP coverage is not claimed.

## Deliberately Unverified Integration Boundary

The standalone capture module was initially tested against synthetic in-memory
fixtures only. The complete companion now also has isolated real Chromium QA
(below). No real Google login, real user profile/cookie, production service or
secret store is accessed. Chrome 132+ is required for document-specific partition
discovery; there is no fallback to older APIs.

The integrating owner must establish user approval and sufficient host/cookie
permissions, including Google parent-domain cookies and HttpOnly cookies.
Chrome filters `getAll` results by granted host permissions, so this module
cannot independently prove permission completeness from those results alone.
Server-side account verification, secure persistence ownership and human Google
login remain integration gates. A page's WIZ identity is browser-observed evidence, not a
cryptographic Google attestation or proof of server-side session validity.

Chrome provides no atomic transaction across tabs, scripting, stores, and cookie
reads. These are bounded before/after observations, not detection of every
transient change that reverts entirely between observations, and not a promise
that the session stays valid after return. No polling, background watcher,
automatic retry, all-profile capture, or runtime browser bridge is introduced
to hide that boundary.

## Isolated Browser QA

```sh
bun --no-env-file run qa
bun --no-env-file run qa:production
```

`playwright-core` is a local, no-browser-download dev dependency. The scripts use
an already installed Playwright Chromium with `channel: chromium`, headless, and
a newly created owned profile. Optionally supply an installed executable path:
`bun --no-env-file run qa.ts /absolute/path/to/chrome`. No global browser install,
Chrome policy bypass, existing user profile or existing CDP endpoint is used.
Branded Chrome may ignore `--load-extension`; prefer installed Playwright Chromium.

`qa` builds a **separate** `dist/qa/` and `-LOCAL-QA.zip` for an ephemeral, explicit
`http://localhost:<port>` portal. The build accepts only a canonical localhost
origin with a port; no arbitrary or wildcard QA origins. Its runtime still checks
that exact origin and path. `bun run build` always defaults back to production.
Manual QA builds can use `bun --no-env-file run build.ts --qa-origin http://localhost:43123`.

The harness routes synthetic Google document URLs in memory, injects only
unprivileged synthetic cookies/Gaia, blocks other requests and external DNS,
and exercises real extension APIs. It records actual extension ID, manifest and
permission grants, `frozen`/partition shapes, original-port delivery, cancellation,
wrong-origin/path/name/state/identity, replay and stale-document rejection.
Screenshots cover actual DOM states at 375/768/1280px in light/dark, keyboard
consent, errors and reduced motion; no token or cookie value is rendered.
The harness never persists a trace or portal session payload.

`qa:production` installs the actual production artifact in another fresh profile
and checks the production-only manifest, Google permissions, localhost denial
and the unbound popup. It does **not** navigate to the production portal or Google.
Reports are `evidence/qa-report.json` and `evidence/production-install.json`.
Browsers, loopback server and temporary profiles are closed/removed in `finally`.
Screenshots are retained as ignored evidence; no production deployment is implied.

For a fresh, isolated LSP pass when the editor's shared workspace times out,
run `bun --no-env-file run lsp-check.ts` with an already installed
`typescript-language-server` on PATH. It opens every owned TypeScript file in a
new language-server process, requires a diagnostic result for every file, writes
`evidence/lsp-report.json`, then shuts down that process. It installs nothing
and changes no editor configuration. `bun run check` remains the strict compiler
gate. CSS/HTML/JSON/Markdown are also checked with the editor's configured LSPs.
