# Gemini Web Native CPA Plugin

Independent C ABI 1 plugin for CPA v7.2.157 (`09a29bd`), using RPC schema 6.
No Go plugin interfaces or core executor changes are required to load the binary.
The backend has no frontend build step; `web/` and `DESIGN.md` are separately owned.
The plugin is deployed alongside `structured-output` on the Plus core. The
Manager resource is at `http://192.168.50.114:18317/management.html#/plugin-pages/gemini-web/0`.
Runtime model requests use direct HTTP; no browser automation is installed in the
sidecar or invoked by this plugin.

## Build And Local Verification

```sh
make check
```

Requires Go 1.26, a C compiler, and Linux with libc. The delivered local artifact
is linux/amd64 with glibc, built with Go 1.26.5; rebuild for a musl/Alpine target.
This runs race-enabled tests,
`go vet`, builds `gemini-web.so` plus its generated header, and loads the actual
shared library with the C harness in `smoke/load.c`. The harness exercises
register/call/error/free/shutdown without any network or credential callbacks.
`build-manifest.json` describes this build; it is not a CPA plugin-store manifest.

The module uses the standard library plus `gopkg.in/yaml.v3` for CPA's YAML
configuration payload. No CPA Go module dependency is linked into the artifact.

## Configuration And Runtime Prerequisites

Plugin ID: `gemini-web`. Plugin configuration keys:

```yaml
vault: homelab
dashboard_path: /CLIProxyAPI/plugins/gemini-web/index.html
```

The vault is intentionally restricted to `homelab`; imported references must
use a 26-character item ID and the unsectioned `web-session` field. Item names,
other vaults, other fields, and query/section suffixes are rejected.

The CPA container needs `op` on PATH and a runtime-injected
`OP_SERVICE_ACCOUNT_TOKEN` with access to the relevant vault. This plugin uses
`op read` itself; it does not assume CPA resolves general `op://` references.
No plaintext credential goes in argv, logs, plugin configuration, or auth JSON.

`compose.yml` reuses the existing host service-account environment and mounts the
existing static `op` binary read-only. The existing Compose wrapper includes this
override; preserve its other configuration and ChatGPT2API injection steps.
`op item create/edit` receives the concealed field through JSON stdin. All stdout
stays in memory; stderr is discarded, and only fixed safe error codes are exposed.
Updates preserve other fields and require a Secure Note item, avoiding passkey
template replacement. A failed CPA save leaves the 1Password item intact for
recovery; no automatic secret/account deletion is performed.

The only upstream is `http://gemini-web2api:8081`. The plugin's own HTTP transport
ignores environment and host proxies, follows no redirects, sets no network
timeouts, and performs no submission retry. Only an internally constructed
`x-goog-api-key` header carries the selected session token. Host HTTP callbacks
are deliberately not used: their stable transport can log requests and apply a
host proxy. Host auth callbacks forward the incoming `host_callback_id`.

## Auth And Models

Registration and token replacement write a reference-only provider record with
`host.auth.save`. Reference-only means no secret values, not a four-field limit:

- `type`: `gemini-web`.
- `id`: `gemini-web-<generated-id>.json`.
- `label`: the account label.
- `token_ref`: `op://homelab/<item-id>/web-session`.
- `disabled`: the preserved host-owned flag, omitted when false.
- `request_scoped_errors`: exactly 300 non-secret rules, ordered by `status`
  from 300 through 599 inclusive. Every rule has `match: ["gemini_web_omni:"]`
  and `action: "stop"`.

The parser rejects token/cookie fields, validates the reference, and registers
the provider with the native host auth pool. Account IDs are also the CPA auth
filenames; selected/pinned IDs must match the record. Host-persisted
`request_scoped_errors` fields are accepted, but these mandatory provider-owned
rules are regenerated rather than trusting external rule contents. Auth parsing
and `auth.refresh` return the same canonical policy in both `StorageJSON` and
runtime metadata. The plugin preserves disabled state and existing account IDs;
token replacement updates the existing secret reference. Updates on a disabled
account return 409 rather than silently enabling it.

`model.for_auth` queries `/v1/account-models`; `available:true` is required.
Static model registration is empty, avoiding uncredentialed fallback accounts.
`gemini-web-flash-3.8` appears only when exactly one actual `3.8 Flash` capability
exists. It maps to `gemini-3.8-flash:generateContent`, supports text/tool emulation,
and returns buffered SSE for streaming. The existing `gemini-web-flash` alias is
never registered or modified. Token counting is explicitly unsupported.

`gemini-web-omni` describes the sidecar's video tool, not a claimed entitlement.
It permits only synchronous Gemini-native, single-user-turn text up to 8000
Unicode characters. Unsupported options, streaming, countTokens, images, and
original chat/Responses payloads are rejected before secret resolution/upstream.
MP4 inlineData is returned unchanged, never converted into an image response.

The registered `request.intercept_before` and `request.intercept_after` hooks
guard only canonical `gemini-web-omni` in either `RequestedModel` or `Model`,
including the host's parenthesized thinking suffix. They use the original
`SourceFormat` and existing Omni body validation, returning successful RPC
results with `Terminate: true`, HTTP 400, and a fixed safe JSON error. RPC errors
would be ignored by the host. Non-Omni and valid native requests return `{}`,
preserving other interceptors' body, header, and structured-output changes.

## Management Contract

All paths below are relative to `/v0/management` and rely on CPA management
authentication. They are not exposed as auth-free resource routes.

- `GET /plugins/gemini-web/accounts`: `{accounts:[{id,label,enabled,status,models:[{id,name}],usage,error?,observed_at}],provider:"gemini-web"}`.
- `POST /plugins/gemini-web/accounts`: `{label,token,existing_id?}` or `{label,token_ref,existing_id?}`; verifies current account availability before writing. Returns `{id,status}`.
- `POST /plugins/gemini-web/refresh`: `{id}`; checks that account only and returns its account-view object. It does not renew Google cookies or launch a browser.

Lists use `host.auth.list`, provider-filtered `get_runtime`, and `get` for the
reference-only record. Health and usage are queried afresh; expired accounts
do not inherit another account's models or measurements. Usage keeps provider
compute units, nullable values, GoogleWeb source, non-estimated measurements,
and observation times. The currently inspected sidecar's `reset_at` is projected
as `reset_unix_seconds`; the latter is also accepted directly. Provider-returned
`ai_credit`/`unknown` windows are preserved, not mislabeled as weekly/5h.

The sole resource is `GET /v0/resource/plugins/gemini-web/index`, menu
`Gemini Web`. It reads the configured HTML file verbatim, with no account data,
headers, tokens, secret references, or server-side rendering. A missing file
returns 503. Query/body/header input cannot invoke management operations here.

## Stable Host Boundaries To Verify Before Deployment

1. ABI errors support `code`, `message`, `http_status`, and `retryable`, but the
   host decoder does not preserve a request-scoped flag or use `retryable` to
   stop account rotation. Canonical auth storage supplies the existing host
   `request_scoped_errors` metadata rules (`action: stop`) for the
   `gemini_web_omni:` error prefix. Omni refuses submission if those rules are
   absent from the selected runtime auth. `host.auth.save` can replace runtime
   metadata directly from saved JSON without invoking the plugin parser, so the
   saved record already contains every rule. Parser/watcher and refresh storage
   round trips retain them without relying on a later host metadata merge. Do
   not add raw tokens or bypass the missing-policy guard to work around activation.
2. The before-auth interceptor sees the original HTTP `SourceFormat` before
   `prepareExecutorCall` rewrites `Format` and `SourceFormat` to the negotiated
   native format. Non-Gemini routes are rejected even with Gemini-shaped bodies;
   executor `OriginalRequest` validation remains in place. Neither interceptor
   nor executor RPC exposes the original downstream `Options.ResponseFormat`.
   Gemini input with an explicit SDK-only alternate response-format override
   therefore remains unobservable through this ABI. Enforcing that combination
   requires host integration outside this plugin; no invented ABI field or core
   patch is included here.
3. C ABI calls have no request cancellation callback. Direct HTTP work can
   outlive a disconnected downstream until the sidecar completes. There are no
   added network timeouts or automatic resubmissions. Validate this lifecycle
   boundary with the target CPA/Plus adapter before enabling video publicly.
4. Stable `host.auth.save/get` are physical auth-file APIs; save also upserts
   the configured auth manager/store. PostgreSQL deployments need that physical
   auth cache and the correct manager-store hookup. Native CPA/Plus loader,
   watcher, and PostgreSQL round trips are the integrating owner's smoke gate.

Tests use injected secret stores, CLI runners, host callbacks, and loopback HTTP
servers. No Google calls, browser sessions, real 1Password access, production
changes, commits, or deployment are performed by the build/tests.

## Account Operations

In Manager, open **Gemini Web** to register or replace a web-session token and
inspect available models and Google-measured quota. The normal Manager admin
login is required; the page does not ask for a separate management credential.
An opaque `gemini-web:v1:` token contains a complete Cookie header and its Google
account index, encoded as base64url JSON. Encoding is not encryption. Keep it in
1Password and enter it only in the masked form, never chat, logs, or a URL.

The five `.220` profiles have separate reference-only CPA records and separate
1Password items. Profile 1's observed Google status was `1016`; its record is
disabled pending a fresh login/session. It is not counted as usable and is not
included in the request pool. Other profiles must still pass live verification
after a token update; registration is not a guarantee of perpetual validity.

Manual refresh checks status and usage; it does not renew cookies or open a
browser. Before submitting an Omni job, the plugin asks the private sidecar to
renew the selected valid session through Google's HTTP RotateCookies endpoint,
verifies the result, and stores a changed token in the same 1Password item before
submission. Renewal or persistence failure stops the request without generating.
There is no background renewal loop and no automatic login for expired sessions.
If a session expires, sign into Gemini in its original profile, obtain a fresh
full session token, and replace it here. For a disabled record, deliberately
enable it through Credential Management before replacing the token; the update
route does not silently enable accounts.
