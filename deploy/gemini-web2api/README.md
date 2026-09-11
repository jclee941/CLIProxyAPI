# Gemini web bridge on 192.168.50.114

This deployment runs [Sophomoresty/gemini-web2api](https://github.com/Sophomoresty/gemini-web2api) at revision `2bb988bfcbb82a7fab5d2c99aa5560ff40d64f7e` as a private sidecar. It does not replace CLIProxyAPI.

## Runtime

- Host files: `/opt/gemini-web2api/`
- Unit: `gemini-web2api.service`
- Container: `cliproxy-gemini-web2api`
- Local diagnostic endpoint: `http://127.0.0.1:8081`
- CLIProxy upstream: `http://gemini-web2api:8081`, on `cliproxyapi_backend`
- API key: the `password` field of the `CLIProxy Gemini bridge (.114)` 1Password item; only its reference is stored here.
- Materialized configuration and cookies: `/run/gemini-web2api/config.json` and `/run/gemini-web2api/cookie.txt`, readable only by root and the container's group.

The host already provides Docker, Compose, `op`, and `/etc/cliproxy/op-service-account.env`. Install these files into `/opt/gemini-web2api/` and the unit into `/etc/systemd/system/`. Build the pinned image before enabling the unit:

```bash
docker build --label org.opencontainers.image.revision=2bb988bfcbb82a7fab5d2c99aa5560ff40d64f7e \
  --tag gemini-web2api:2bb988b \
  'https://github.com/Sophomoresty/gemini-web2api.git#2bb988bfcbb82a7fab5d2c99aa5560ff40d64f7e'
systemctl daemon-reload
docker build --tag gemini-web2api:web-http-20260911 /opt/gemini-web2api
systemctl enable --now gemini-web2api.service
```

`systemctl reload gemini-web2api.service` re-injects secrets and recreates only the bridge, since its configuration is loaded at process startup. The unit regenerates the `/run` configuration after reboot.

## Web session

The bridge uses the default signed-in Google web account, not Gemini API credentials or Antigravity OAuth. Its session is stored in the `CLIProxy Gemini web session (.114)` 1Password item (`viegkixstxaq2l6c4eadyrsaou`):

- `cookie` (concealed): the complete single-line cookie header from the same Gemini web session. Do not reduce it to a fixed subset: omitting other Google session cookies can produce guest responses even when text generation returns HTTP 200.
- `xsrf_token` (concealed): the same session's `SNlM0e` value.
- `gemini_bl`: the same page's frontend build identifier (`cfb2h`).

Refresh these fields from the same signed-in [Gemini web account](https://gemini.google.com/app) when the session expires, then reload the service. Do not paste their values into chat or shell arguments. Install `cookie.txt.tpl` alongside `config.json.tpl` before reloading. The start script stages both files with restricted permissions before replacing the runtime files. The existing read-only `/run/gemini-web2api` mount makes the cookie available to the container without another mount.

`auth_user: null` selects the default account path. A non-default `/u/<index>/` session requires the matching `auth_user` configuration and session values. Cookie refresh does not alter the existing CLIProxy model aliases.

## CLIProxy Gemini provider

Merge this entry into the existing `gemini-api-key` list in `/opt/dashboard/config.local.yaml`, preserving all other entries:

```yaml
gemini-api-key:
  - api-key: op://homelab/2n2nev5cbn3bork76itu7k6pqq/password
    base-url: http://gemini-web2api:8081
    proxy-url: direct
    models:
      - name: gemini-3.7-flash
        alias: gemini-web-flash
```

The Gemini executor appends `/v1beta`, so the base URL must not include it. The source template is injected into `/run/cliproxy/config.yaml`. The existing injector replaces that file's inode; apply through `/usr/local/sbin/cliproxy-compose up -d --no-deps --force-recreate cliproxyapi` so the container mounts the updated file. This briefly restarts CLIProxyAPI; do not restart unrelated services.

The entry appears under Gemini in `/management.html#/ai-providers`. Clients request `gemini-web-flash` through `/v1/chat/completions`; both regular and streaming text requests have been verified on this host.

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

The extension image continues to inherit the existing pinned image. The development
environment does not change the runtime or install browser automation.

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
