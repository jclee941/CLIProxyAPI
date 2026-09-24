# Gemini Web Native CPA Plugin

Independent C ABI plugin using encrypted local application sessions. Build and
load it without an external secret CLI, Vault service, or browser-driven model
execution.

## Build

```sh
make check
```

Requires Go 1.26+, a C compiler, and a compatible libc. The gate runs race-enabled
tests, `go vet`, builds `gemini-web.so`, and exercises the shared-library ABI.
Build `web/index.html` separately using the commands in [web/README.md](web/README.md).

## Configuration

```yaml
dashboard_path: /CLIProxyAPI/plugins/gemini-web/index.html
session_dir: /var/lib/gemini-web/sessions
manager_origin: https://cliproxy.jclee.me
browser_extension_id: REPLACE_WITH_REGISTERED_EXTENSION_ID
native_generation: true
native_continuation: true
```

The running process needs `GEMINI_WEB_SESSION_KEY`. On the deployed host,
`deploy/gemini-web-local/start-production.sh` reads the mounted
`/run/gemini-web-local/session.key`; its protected host source is
`/etc/cliproxy/gemini-web-local/session.key`. Preserve this existing key and the
encrypted session directory together. Never generate a replacement key during an
upgrade. Directory mode is `0700`; key and session file modes are `0600`.

`compose.yml` is a no-op overlay retained for existing Compose command lists.
It adds no secret-provider environment or executable mount. The current native
production path does not require a sidecar. Legacy sidecar code and deployment
files remain in the repository; their presence is not a reason to start them.

## Login and account management

Use the authenticated Manager portal and companion extension. Login starts at
`POST /v0/management/plugins/gemini-web/login/start` and completes through the
matching login flow. Do not create accounts through the removed token-registration
route or write session records manually.

Host auth records contain opaque `session://gemini-web/<32hex>` references and
non-secret metadata. Credentials stay in the encrypted local store. Existing
account IDs and disabled flags must be preserved during upgrades.

See [LOCAL-SESSIONS.md](LOCAL-SESSIONS.md) for the persistence contract and
[ops/README.md](ops/README.md) for optional maintenance. The maintenance launcher
uses the existing protected local `core.env` management credential. Installing
files does not enable the timer.

## Video API

The consumer contract is published at
[`https://cliproxy.jclee.me/openapi.json`](https://cliproxy.jclee.me/openapi.json).
The same document is embedded at
`/v0/resource/plugins/gemini-web/openapi.json`; the management page links to the
canonical URL.

- Create: `POST /v1beta/interactions`.
- Extend: use a completed `previous_interaction_id` and
  `generation_config.video_config.task="extend"`.
- Retrieve: `GET /v1beta/interactions/{id}` with the same API key.
- Default framing is portrait, `9:16`; explicit `16:9` remains supported.

Check `status="completed"` and video content before consuming or extending a
result. A lost POST response is not authorization to submit the same job again.

## Deployment

The host is `192.168.50.114`; the plugin directory is
`/opt/dashboard/plugins`, mounted at `/CLIProxyAPI/plugins`. The static page is
`/opt/dashboard/plugins/gemini-web/index.html`.

Run the build and ABI gates before replacing the artifact. Inspect actual active
requests before a restart. Local session storage has a process-lifetime owner
lock: loading another plugin instance can fail with
`session_store_already_owned`. Do not delete lock/session files. A coordinated
container restart releases the old owner.

For management checks, load `/etc/cliproxy/gemini-web-local/core.env` only on the
host and use `MANAGEMENT_PASSWORD` in memory. Do not print the environment or put
credentials in command arguments, documents, or logs.
