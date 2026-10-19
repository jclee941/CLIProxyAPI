# Gemini Web Native CPA Plugin

Independent C ABI plugin using encrypted local application sessions. Build and
load it without an external secret CLI, Vault service, or browser-driven model
execution.

## Build

```sh
make check
```

Requires Go 1.26+, a C compiler, and a compatible libc. The gate runs race-enabled
tests and `go vet`, then builds a throwaway `abi-smoke.so` and exercises the
shared-library ABI. It leaves the committed `gemini-web.so` alone: that artifact
is the Debian bookworm build the NAS container loads.
Build `web/index.html` separately using the commands in [web/README.md](web/README.md).

## Configuration

```yaml
dashboard_path: /CLIProxyAPI/plugins/gemini-web/index.html
session_dir: /var/lib/gemini-web/sessions
manager_origin: https://cliproxy.jclee.me
browser_extension_id: REPLACE_WITH_REGISTERED_EXTENSION_ID
native_continuation: true
video_wire_mode: legacy
video_mode: 0
delete_chats: false
```

The plugin speaks to the Gemini web product directly; there is no sidecar path.
`native_generation` is accepted only as `true`, so configurations written before
the sidecar path was removed keep loading. Leave it out of new configurations.

`video_wire_mode` accepts `legacy` (the default, unchanged request bytes), `web`,
or `alternate`. For Omni video only, `web` sends selection-header capacity `3`,
capabilities `[4,5,6,8,16,4,5,6,8,16]`, and body slot 30 `[4,16]` when the
serving account advertises capacity flag `16`. Other accounts keep legacy bytes;
text and image requests are unaffected.

`alternate` hashes the first local receipt to split new conversations. The
chosen variant is stored with the turn and inherited by extensions and edits,
including after a restart. Existing conversations without a recorded variant
stay legacy. Explicit `legacy` or `web` overrides the stored choice, so rollback
does not have to wait for a new conversation. `video_turn_diag` reports the
effective `wire=legacy|web` in `reason`.

Use the authenticated `PATCH /v0/management/plugins/gemini-web/config` with
`{"video_wire_mode":"alternate"}` (or `legacy` / `web`). The patch preserves other
plugin settings. Confirm the saved value with the matching GET and the applied
variant in real video diagnostics; a saved value alone is not reload evidence.
Changing only this policy does not drain or replace live sessions. A successful
change also emits `state=video_wire_mode reason=mode=<value>` in the host log.

`video_mode` is the capability mode an Omni video turn runs on when its request
names no `generation_config.video_mode`. `0` (the default) keeps each account's
standard mode, currently 3.8 Flash; `8` selects 4 Argon. An account that does not
advertise the configured mode uses its standard mode, while a mode named in the
request still has to be advertised. Set it with the same PATCH, for example
`{"video_mode":8}`; it does not drain sessions and logs
`state=video_mode reason=mode=<value>`.

`delete_chats` (default `false`) deletes the web conversation a text or music
turn opened once it has answered, keeping generated chats out of the account's
history. Toggle it live with
`PUT /v8/management/config/plugins/configs/gemini-web/delete_chats` and a JSON
`true`, and `DELETE` the same path to return to the default; the host log shows
`state=delete_chats reason=enabled=<value>`. It does not drain sessions.

The running process needs `GEMINI_WEB_SESSION_KEY`. On the deployed host,
`deploy/gemini-web/start-production.sh` (installed on the NAS as
`/volume1/docker/cliproxy/runtime/start-production.sh`) reads the mounted
`/run/gemini-web/session.key`; its protected host source is
`/volume1/docker/cliproxy/runtime/secrets/session.key`. Preserve this existing key and the
encrypted session directory together. Never generate a replacement key during an
upgrade. Directory mode is `0700`; key and session file modes are `0600`.

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

## Text and music models

Each account publishes a text model per Gemini web mode it offers, named
`gemini-app-<version>-<mode>`: currently `gemini-app-3.5-flash-lite`,
`gemini-app-3.8-flash` and `gemini-app-4-argon`. They answer `generateContent`
and `/v1/chat/completions`. `generationConfig.thinkingConfig.thinkingLevel`
accepts `low`, `medium` and `high`; any other value is refused with
`invalid_thinking_level`. Code the web app ran comes back as ordinary fenced
blocks. The 3.1 Pro mode is not published: every text turn in it failed.

`gemini-lyria-3.5` makes a music track through `generateContent`. The answer
carries the web app's text, the track as `audio/mpeg` and its cover video as
`video/mp4`, both as `inlineData`; a turn that makes no track is refused with
`no_music_generated` and the web app's reply.

Image generation is not offered.

## Deep Research

`gemini-deep-research` runs the Gemini app's Deep Research agent, in the
account's 4 Argon mode, through `POST /v1beta/interactions`. It needs
`background: true` and refuses `store: false` and `stream: true`. By default the
agent's plan starts on its own and the create answers `status: "in_progress"`;
`GET /v1beta/interactions/{id}` returns the Markdown report, ending in a Sources
list, once it is `completed`. With `agent_config.collaborative_planning: true` the
create completes with the plan instead, and creating again with
`previous_interaction_id` set to the plan starts it. A completed report takes
follow-up questions the same way. The contract is in `openapi.json`.

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
`store: false` returns the video without keeping it: its id then answers 404
`interaction_not_found`, and the turn's web conversation is deleted after
delivery. `generation_config.temporary_chat` is refused with
`omni_temporary_chat_unobservable`: the web app cannot read a temporary chat's
video back.

## Deployment

Deployment follows [deploy/README.md](../README.md): the committed artifact is
converged onto the NAS, whose plugin directory
`/volume1/docker/cliproxy/runtime/plugins` is mounted at `/CLIProxyAPI/plugins`.
The static page is `/CLIProxyAPI/plugins/gemini-web/index.html`.

Run the build and ABI gates before replacing the artifact. Inspect actual active
requests before a restart. Local session storage has a process-lifetime owner
lock: loading another plugin instance can fail with
`session_store_already_owned`. Do not delete lock/session files. A coordinated
container restart releases the old owner.

For management checks, load `/volume1/docker/cliproxy/runtime/secrets/core.env` only on the
host and use `MANAGEMENT_PASSWORD` in memory. Do not print the environment or put
credentials in command arguments, documents, or logs.
