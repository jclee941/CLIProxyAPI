# Gemini web bridge on 192.168.50.114

This retired deployment used [Sophomoresty/gemini-web2api](https://github.com/Sophomoresty/gemini-web2api) at revision `2bb988bfcbb82a7fab5d2c99aa5560ff40d64f7e` as a private sidecar. It is not an active container on `.114`. The launcher, unit, and Compose snapshot are retained historical artifacts, not a supported procedure to revive the sidecar during CPA credential migration.

The Runtime, Web session, and CLIProxy Gemini provider sections retain the
historical shared-key deployment and its verification evidence. They are not
the renewal procedure for the native multi-account Gemini Web plugin. Its new
maintenance implementation must be integrated and validated on the host before
being described as deployed; see [maintenance operations](../gemini-web-plugin/ops/README.md).

## Local File Contract (Retained Launcher Only)

`start.sh` requires existing, resolved `/etc/cliproxy/gemini-web2api/config.json`
and `/etc/cliproxy/gemini-web2api/cookie.txt`. An operator migrates the existing
values unchanged into root-owned `0700` source directories with root:root `0600`
files. Startup does not source a service-account environment, invoke `op`, fall
back to a vault, generate credentials, or rotate them. The blank `.tpl` files are
examples only, not valid credentials; the cookie template is deliberately empty.

Both inputs must be readable, nonempty regular files before any copy or Docker
invocation. They are staged as `.next` files, root:65532 `0640`, then moved into
the existing root:65532 `0750` `/run/gemini-web2api` directory. The existing mounts
and Compose command are unchanged. **Do not run this launcher, enable the unit,
or restart the retired bridge as part of the active CPA migration.**

For isolated tests only, `CLIPROXY_CONFIG_DIR` overrides the `/etc/cliproxy` root
and `GEMINI_WEB2API_RUN_DIR` overrides the runtime directory; Compose mount paths
are unchanged. Shared startup tests are in `../chatgpt2api/tests/` and use fake
Docker and an `op` trap without live credentials.

## Historical Runtime

- Host files: `/opt/gemini-web2api/`
- Unit: `gemini-web2api.service`
- Container: `cliproxy-gemini-web2api`
- Local diagnostic endpoint: `http://127.0.0.1:8081`
- CLIProxy upstream: `http://gemini-web2api:8081`, on `cliproxyapi_backend`
- API key: formerly the `password` field of the `CLIProxy Gemini bridge (.114)` 1Password item; retained startup now reads the same resolved value from the protected local config.
- Materialized configuration and cookies: `/run/gemini-web2api/config.json` and `/run/gemini-web2api/cookie.txt`, readable only by root and the container's group.

The former deployment used Docker and Compose under `/opt/gemini-web2api/` with
the unit in `/etc/systemd/system/`. These commands are historical build/install
evidence, not current rollout instructions; do not execute them for this migration:

```bash
docker build --label org.opencontainers.image.revision=2bb988bfcbb82a7fab5d2c99aa5560ff40d64f7e \
  --tag gemini-web2api:2bb988b \
  'https://github.com/Sophomoresty/gemini-web2api.git#2bb988bfcbb82a7fab5d2c99aa5560ff40d64f7e'
systemctl daemon-reload
docker build --tag gemini-web2api:web-http-20260911 /opt/gemini-web2api
systemctl enable --now gemini-web2api.service
```

The retained reload action would copy the local files and recreate only the bridge,
since configuration is loaded at process startup; it is not part of the active
rollout. The retired unit must remain inactive. Native plugin account-cookie
updates are separate and do not require restarting this legacy bridge.

## Historical Web Session

The bridge used the default signed-in Google web account, not Gemini API credentials or Antigravity OAuth. Its session was stored in the `CLIProxy Gemini web session (.114)` 1Password item (`viegkixstxaq2l6c4eadyrsaou`):

- `cookie` (concealed): the complete single-line cookie header from the same Gemini web session. Do not reduce it to a fixed subset: omitting other Google session cookies can produce guest responses even when text generation returns HTTP 200.
- `xsrf_token` (concealed): the same session's `SNlM0e` value.
- `gemini_bl`: the same page's frontend build identifier (`cfb2h`).

Historically these fields came from the same signed-in [Gemini web account](https://gemini.google.com/app). Do not paste their values into chat or shell arguments. The retained launcher now reads resolved local `config.json` and `cookie.txt`, not templates. It stages both files with restricted permissions before replacing the runtime files. The historical read-only `/run/gemini-web2api` mount made the cookie available without another mount. No session refresh or bridge reload is required for the current CPA migration.

`auth_user: null` selects the default account path. A non-default `/u/<index>/` session requires the matching `auth_user` configuration and session values. Cookie refresh does not alter the existing CLIProxy model aliases.

## Historical CLIProxy Gemini Provider

This was the legacy `gemini-api-key` entry. Do not re-add a route to the retired
sidecar. Active CPA configuration is now the resolved local
`/etc/cliproxy/core/config.yaml`, not an injected template. The blank key below
is an example only; the former route used the same key as the bridge config:

```yaml
gemini-api-key:
  - api-key: ""
    base-url: http://gemini-web2api:8081
    proxy-url: direct
    models:
      - name: gemini-3.7-flash
        alias: gemini-web-flash
```

The Gemini executor appends `/v1beta`, so the base URL must not include it. The CPA
wrapper now copies the protected core config into `/run/cliproxy/config.yaml`.
That copy replaces the file's inode, so an approved active CPA rollout must
recreate only `cliproxyapi` through its installed wrapper and preserve all deployed
Compose overlays. Do not substitute this retired sidecar's Compose snapshot or
restart unrelated services.

The entry appeared under Gemini in `/management.html#/ai-providers`. Clients
requested `gemini-web-flash` through `/v1/chat/completions`; both regular and
streaming text requests were verified on this host. This is historical evidence,
not a claim that the retired sidecar still serves requests.

## Boundaries

This configuration supplies a Google web-session snapshot. Google can invalidate that snapshot when browser cookies rotate. The pinned bridge can silently route Pro requests to Flash when the account lacks the required entitlement, and its model-list endpoint does not validate the session. Verify the authenticated account status and account model registry after refreshing credentials; do not treat a model listing, text response, or HTTP 200 alone as proof of paid Pro access.

`gemini-3.7-flash` is the pinned bridge's advertised web-mode alias, not independent proof of Google's underlying model version. Do not register Pro or video capabilities on the strength of an alias. The unmodified pinned image does not implement video jobs, polling, or video retrieval. The separate `extension/` implementation and Gemini Web plugin must be validated and deployed explicitly for those capabilities.

Request logging is disabled, the container runs unprivileged with a read-only filesystem, and the diagnostic port is loopback-only. Keep Google credentials out of this repository, shell arguments, logs, and Telegram.

## Extension Development

The pinned upstream runs directly from its source tree in Docker; its wheel build
does not declare package discovery for its multiple top-level directories. Use a
checkout of revision `2bb988bfcbb82a7fab5d2c99aa5560ff40d64f7e` as the module path,
matching the image, rather than treating that repository as an installable wheel:

```bash
uv sync --frozen
PYTHONPATH=/path/to/pinned/gemini-web2api uv run pytest tests
```

The extension image continues to inherit the existing pinned image. Model
generation remains pure HTTP. Credential maintenance's separately bounded
read-only CDP recovery is not browser-driven generation or an interactive login
workflow, and does not change the legacy shared-key route.

## Account-Aware HTTP Extension

Compose starts `python -m extension`, not the original single-account module.
The original shared-key `gemini-web-flash` route remains compatible. The separate
native CPA plugin uses account-specific opaque tokens and exposes
`gemini-web-flash-3.8` and `gemini-web-omni` through CPA.

- `GET /v1/account-models` verifies the selected Google account and its actual
  model capabilities. `3.8 Flash` must exist in that account's registry; there is
  no fallback to an older model under the new identifier.
- `GET /v1/usage` returns GoogleWeb measurements, observation time, 5-hour/week
  windows and remaining provider compute units. These are not token counts or
  currency. Missing data remains unknown.
- `POST /v1/session/renew` is a private credential-acquisition endpoint. It accepts
  only an account token and an empty body, contacts the fixed Google cookie
  endpoint, validates the returned account, and returns a no-store replacement
  token to the plugin. The shared legacy key cannot use this endpoint, and it is
  not exposed through the plugin's generic HTTP executor.
- Native video requests submit the observed web video-tool fields and poll only
  that conversation operation. Completed MP4s are fetched from allowlisted Google
  origins. The RPC decoder measures Google's UTF-16 frame lengths, including
  non-ASCII text. No failed generation is automatically resubmitted.

The upstream web interface is unofficial and can change. Model names, a 200
response, or a live browser connection are not proof of account entitlement.
Account expiration is reported, not replaced with another account's status.

## Historical Native Session Maintenance

The following records the earlier maintenance design and is not the current
CPA local-file runbook. It does not establish an active sidecar or authorize
installation/restart of these retired launchers.

One external `.114` systemd timer invokes the authenticated plugin
`POST http://127.0.0.1:8317/v0/management/plugins/gemini-web/maintain` with `{}`
every five minutes after the previous invocation finishes. It is not a sidecar
or plugin background loop, and does not rely on CPA-core 401 handling. The
runner reads only a runtime-injected management key, makes one request, prints
state counts only, and neither logs response bodies nor retries failures.
Dashboard Refresh continues to inspect status/usage without credential renewal.

The plugin owns the AuthID-to-`maintenance_sources` map and the single-writer
credential lifecycle. See its [configuration schema](../gemini-web-plugin/README.md#configuration-and-runtime-prerequisites)
for synthetic reference/GUID/Gaia-hash examples; never commit the real five
bindings. Model requests use HTTP only. Maintenance first attempts HTTP cookie
rotation for a valid token. Only explicit HTTP authentication failure can invoke
read-only capture from an existing Gemini page at fixed `.220:9222`, never on
permission-denied 403, 429, network, or parser errors. No open/login/2FA/clear/switch
actions are allowed. Configured, browser-observed, and HTTP-verified Gaia hashes
must agree; missing identity or multiple contexts fails closed. Tab/context IDs
are not pinned, and physical profile GUIDs do not attest current account identity.

The Python credential worker has a 60-second kill-and-wait budget; Go retains a
70-second fence after a lost response. Authentication failures cause a 30-minute
per-reference cooldown, not automatic relogin. Unknown write/submission outcomes
require operator intervention without automatic retry. `host_sync_pending`
retains the newer token in 1Password and retries only host synchronization on a
later cycle, without rollback. Disabled Profile 1 is never automatically enabled.

1Password expected-token comparison is not CAS; stale item versions can overwrite
newer values. Allow only one active plugin writer. Before 1Password UI, CPA-core,
or direct reference changes, stop the timer and drain maintenance, Flash, Omni,
and manual credential writes. Do not disrupt the existing Plus, Manager,
ChatGPT2API, or legacy `gemini-web-flash` configuration. Installation and
fresh-process verification are operator steps, not completed deployment claims.
