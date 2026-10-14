# Deployment

The GitOps convergence loop for this CLIProxyAPI fork, plus ancillary packages the current host does not deploy:

- [telegram-log-forwarder](telegram-log-forwarder/README.md) — forwards new `error-*.log` entries to Telegram.
- [telegram-usage-bot](telegram-usage-bot/README.md) — Telegram bot that reports CLIProxyAPI usage.

## GitOps model

The fork's `master` branch is the single source of truth for what runs on the host.

1. The [`core-build`](../.github/workflows/core-build.yml) workflow tests `master` whenever a push changes anything the core tests read, and builds the CPA core and publishes it on the `cpa-core` release when the core itself changed; see [CPA core](#cpa-core).
2. The host converger [`pull-deploy.sh`](pull-deploy.sh) is run on the host after `master` moves; see [Running a deploy](#running-a-deploy). It fetches the fork branch into a dedicated deploy clone.
3. Each run then applies [`cpa-converge.py`](cpa-converge.py) with [`cpa-plugins.json`](cpa-plugins.json) to the CPA core and native plugins; see [CPA native plugins](#cpa-native-plugins).

[`upstream-release-sync`](../.github/workflows/upstream-release-sync.yml) checks hourly for a new `router-for-me/CLIProxyAPI` release. It merges the release into `master` only when the merge is clean and `go test ./...` passes on `golang:1.26-bookworm`, then dispatches `core-build`. A conflict or a red test leaves `master` unchanged and sends one Telegram alert per release through the repository secrets `TELEGRAM_BOT_TOKEN` and `TELEGRAM_CHAT_ID`. Run it by hand to retry a release, or with `test_alert` to check the alert channel. Deploying stays an explicit `pull-deploy.sh` run.

Nothing pushes to the host. The host pulls. To roll back, move `master` in the fork and run `pull-deploy.sh` again.

## Current host

Since 2026-10-06, CPA runs on the Synology DS423+ at 192.168.50.215 (DSM Container Manager, Docker 24). The former LXC host 192.168.50.114 is stopped. Everything lives under `/volume1/docker/cliproxy`:

| Path | Contents |
| --- | --- |
| `deploy/` | Dedicated deploy clone |
| `runtime/` | Core executable, plugin directory, `config.yaml`, `sessions/`, `auths/`, `secrets/`, and the core compose file |
| `manager/` | CPA Manager Plus, published on `192.168.50.215:18317` |

The NAS Cloudflare tunnel routes `cliproxy.jclee.me` and `cliproxy2.jclee.me` itself; no reverse proxy sits in front of CPA. Requests for `/`, `/management.html`, `/health`, `/status`, `/setup`, `/models`, `/usage-service/*`, `/v0/management/*` and `/v0/resource/plugins/*` go to Manager Plus on `:18317`, and everything else goes to the core on `:8317`. A Cloudflare URL rewrite rule serves `/openapi.json` from `/v0/resource/plugins/gemini-web/openapi.json`.

The DSM kernel has no CFS quota, so compose files there must not set `cpus:`. On the NAS, `sudo` asks for a password, so run the converger as `jclee` (a member of the `docker` group). A non-interactive ssh shell lacks `/usr/local/bin`, where `docker` lives, so put it on `PATH`. The converger defaults are this host's paths:

```bash
B=/volume1/docker/cliproxy
PATH=/usr/local/bin:$PATH GITOPS_DEPLOY_DIR=$B/deploy $B/deploy/deploy/pull-deploy.sh >> $B/pull-deploy.log 2>&1
```

## CPA native plugins

[`cpa-plugins.json`](cpa-plugins.json) declares what the CPA container serves from its plugin directory: each plugin's committed `.so` artifact and each dashboard file. Deploying a plugin is committing its bookworm-built artifact to `master`; nothing else is needed.

On every tick `cpa-converge.py` compares each artifact in the deploy clone with the live copy by SHA-256 and changes only what differs:

| Kind | How it is applied |
| --- | --- |
| Dashboard file | Replaced atomically. The host reads it per request. |
| Plugin, `"reload": "hot"` | Installed as the only loadable `<id>-v0.1.0.<date>.g<commit>.so`, then re-enabled with `PATCH /v0/management/plugins/<id>/enabled`. The host loads a swapped file only when `config.yaml` changes, and re-enabling an enabled plugin writes the same bytes, so the plugin's `priority` is raised by one until `main.log` shows `plugin loaded plugin_id=<id> version=<new version>`, then put back. The plugin listing names the file on disk whether or not it is loaded, so the converger checks that log line instead. |
| Plugin, `"reload": "restart"` | Swapped under `docker stop -t 240` / `docker start`, only after the host shows no Omni turn and no API request for 65 seconds: no unanswered Omni pick in the last 12 minutes of `main.log`, no account generating, and no request counted by `GET /v0/management/in-flight`, since a restart cuts every chat and image request the host is answering. A core older than that count answers 404, so the restart that installs a counting core is gated on Omni turns alone. A busy host leaves the swap to a later tick. |

The replaced file stays next to the new one as `<old name>.prev-g<commit>`, which the host does not load. A plugin the host does not report as loaded within the health wait gets one `docker restart`; if it still does not load, the tick fails and nothing is rolled back. Compose is never used for the CPA container: its image cannot be pulled on the host, and a compose run that fails to pull takes the container off its network.

Configuration, all optional:

| Variable | Default |
| --- | --- |
| `CPA_PLUGIN_DIR` | `/volume1/docker/cliproxy/runtime/plugins` |
| `CPA_CONTAINER` | `cliproxyapi` |
| `CPA_API` | `http://127.0.0.1:18318` |
| `CPA_CORE_ENV` | `/volume1/docker/cliproxy/runtime/secrets/core.env` (read for `MANAGEMENT_PASSWORD`) |
| `CPA_CORE_BINARY` | `/volume1/docker/cliproxy/runtime/CLIProxyAPI` |
| `CPA_DRY_RUN` | unset; `1` prints the plan and changes nothing |

Check a host before enabling it:

```bash
cd /volume1/docker/cliproxy/deploy && PATH=/usr/local/bin:$PATH CPA_DRY_RUN=1 python3 deploy/cpa-converge.py deploy/cpa-plugins.json
```

Tests: `cd deploy && python3 -m unittest cpa_converge_test`.

## CPA core

The container bind-mounts the core executable from `/volume1/docker/cliproxy/runtime/CLIProxyAPI`. `master` decides its version too, but the executable is too large to commit, so [`core-build`](../.github/workflows/core-build.yml) builds it and `cpa-converge.py` installs the build.

The `core` entry of [`cpa-plugins.json`](cpa-plugins.json) names the core paths, the release the builds are published on, and the asset prefix. The core commit is the last first-parent `master` commit that touched those paths; the workflow and the converger resolve it with the same `git log` query. The paths leave out docs (`*.md`), tests (`*_test.go`) and `testdata`, which do not change the executable, so a commit that changes only those keeps the current core and restarts nothing.

- `core-build` runs by hand and on pushes to `master` that touch anything the core tests read: the core paths and their tests, any Go file (the no-copy invariant in `internal/util` scans them all), `deploy/gemini-web-plugin` (built from source by `internal/pluginhost` tests), `config.example.yaml`, and `cpa-plugins.json`. Docs-only pushes do not start it. Every run tests `master` with `go test ./...` with cgo on `golang:1.26-bookworm`, the container's Debian 12 base. For a core commit without a published build, it then builds `./cmd/server` from that commit, with the commit stamped into the build, publishes `CLIProxyAPI-<commit>` and `CLIProxyAPI-<commit>.sha256` on the `cpa-core` prerelease, and keeps the newest ten builds. A run whose tests fail publishes nothing.
- `cpa-converge.py` compares the published SHA-256 of that build with the live executable; builds are not byte-reproducible, so the published build decides. When they differ, it downloads and verifies the build before anything stops, then swaps it in the same idle-gated `docker stop -t 240` / `docker start` window as the restart plugins, so a core change and a plugin change share one restart. The host must then report the new commit in `X-CPA-COMMIT`, every manifest plugin loaded, and as many ready accounts as before. Otherwise it gets one `docker restart`, and the run fails without a rollback. The replaced executable stays as `CLIProxyAPI.prev`.
- A core commit whose build is not published yet fails the run before anything changes. Run `pull-deploy.sh` again after `core-build` finishes.

## One-time host setup

```bash
git clone --branch master https://github.com/jclee941/CLIProxyAPI.git /volume1/docker/cliproxy/deploy
chmod +x /volume1/docker/cliproxy/deploy/deploy/pull-deploy.sh
```

The deploy clone is reset with `git reset --hard` on every update. Use it only for deployment; never edit code inside it. The converger aborts if `git status --porcelain` is non-empty.

## Running a deploy

There is no cron entry; deploys are explicit. After `master` moves, and after `core-build` has published any core change, run the converger on the host:

```bash
B=/volume1/docker/cliproxy
PATH=/usr/local/bin:$PATH GITOPS_DEPLOY_DIR=$B/deploy $B/deploy/deploy/pull-deploy.sh >> $B/pull-deploy.log 2>&1
```

A run that finds an Omni turn in flight leaves its restart to the next run. Overlapping runs are safe: the script takes a non-blocking `flock` on a lock file next to the deploy directory and skips the run if another one is active.

### Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `GITOPS_DEPLOY_DIR` | *(required)* | Dedicated deploy clone |
| `GITOPS_REMOTE` | `https://github.com/jclee941/CLIProxyAPI.git` | Git remote to fetch |
| `GITOPS_BRANCH` | `master` | Branch to converge on |
| `GITOPS_LOCK_FILE` | `<deploy dir>.pull-deploy.lock` | Lock file path |
| `GITOPS_CPA_MANIFEST` | `deploy/cpa-plugins.json` | CPA manifest relative to the deploy clone, applied by `cpa-converge.py` on every run |

`pull-deploy.log` is not rotated by the script. Add a `logrotate` entry if it grows.
