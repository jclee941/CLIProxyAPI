# Gemini web bridge on 192.168.50.114

This deployment runs [Sophomoresty/gemini-web2api](https://github.com/Sophomoresty/gemini-web2api) at revision `2bb988bfcbb82a7fab5d2c99aa5560ff40d64f7e` as a private sidecar. It does not replace CLIProxyAPI.

## Runtime

- Host files: `/opt/gemini-web2api/`
- Unit: `gemini-web2api.service`
- Container: `cliproxy-gemini-web2api`
- Local diagnostic endpoint: `http://127.0.0.1:8081`
- CLIProxy upstream: `http://gemini-web2api:8081`, on `cliproxyapi_backend`
- API key: the `password` field of the `CLIProxy Gemini bridge (.114)` 1Password item; only its reference is stored here.
- Materialized configuration: `/run/gemini-web2api/config.json`, readable only by root and the container's group.

The host already provides Docker, Compose, `op`, and `/etc/cliproxy/op-service-account.env`. Install these files into `/opt/gemini-web2api/` and the unit into `/etc/systemd/system/`. Build the pinned image before enabling the unit:

```bash
docker build --label org.opencontainers.image.revision=2bb988bfcbb82a7fab5d2c99aa5560ff40d64f7e \
  --tag gemini-web2api:2bb988b \
  'https://github.com/Sophomoresty/gemini-web2api.git#2bb988bfcbb82a7fab5d2c99aa5560ff40d64f7e'
systemctl daemon-reload
systemctl enable --now gemini-web2api.service
```

`systemctl reload gemini-web2api.service` re-injects secrets and recreates only the bridge, since its configuration is loaded at process startup. The unit regenerates the `/run` configuration after reboot.

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

This initial configuration has **no Google account session**. It uses the upstream project's anonymous text path. No browser cookies posted in chat were stored or used. A successful text response does not prove paid Google account access.

`gemini-3.7-flash` is the pinned bridge's advertised web-mode alias, not independent proof of Google's underlying model version. Do not register Pro or Veo capabilities on the strength of an alias. This bridge does not implement Veo video jobs, polling, or video retrieval. Video generation requires a different supported backend.

Request logging is disabled, the container runs unprivileged with a read-only filesystem, and the diagnostic port is loopback-only. Keep Google credentials out of this repository, shell arguments, logs, and Telegram.
