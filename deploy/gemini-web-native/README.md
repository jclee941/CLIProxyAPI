# Retired Gemini Web Native Launcher

This sidecar is not active on `.114`. The launcher and Compose snapshot are
historical, unsupported deployment artifacts, not instructions to enable or
rebuild the service. No image, container, or service is installed by this change.
In particular, the old cookie-based `config.json.tpl` is not the current
browser-backed application's schema; its blank fields are archival examples.

The retained `start.sh` no longer uses `op` or a service-account environment. It
requires an existing resolved `/etc/cliproxy/gemini-web-native/config.json` and
copies it to `/run/gemini-web-native/config.json`. An operator supplies the local
file (root:root `0600` inside root-owned `0700` source directories); it is never
generated, refreshed, or rotated by startup. Missing, unreadable, empty, or
non-regular inputs fail before Docker.

The historical runtime permissions are unchanged: root:65532 `0750` for the
directory, root:65532 `0640` for config, and 65532:65532 `0700` for `cookies/`.
The Compose command is unchanged and would recreate the retired service, so do
not run the launcher on the host as part of CPA credential migration.

Isolated tests may override `CLIPROXY_CONFIG_DIR` (the `/etc/cliproxy` root) and
`GEMINI_WEB_NATIVE_RUN_DIR` (the runtime directory). Compose mount paths are not
changed. The shared deterministic tests live in `../chatgpt2api/tests/` and use
synthetic local files, a fake Docker command, and an `op` trap.
