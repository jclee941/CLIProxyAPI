# Deployment

Sidecars and the GitOps convergence loop for this CLIProxyAPI fork.

- [telegram-log-forwarder](telegram-log-forwarder/README.md) — forwards new `error-*.log` entries to Telegram.
- [telegram-usage-bot](telegram-usage-bot/README.md) — Telegram bot that reports CLIProxyAPI usage.

## GitOps model

The fork's `master` branch is the single source of truth for what runs on the host.

1. The [`core-build`](../.github/workflows/core-build.yml) workflow tests and builds the CPA core whenever `master` changes it and publishes the build on the `cpa-core` release; see [CPA core](#cpa-core).
2. The host converger [`pull-deploy.sh`](pull-deploy.sh) is run on the host after `master` moves; see [Running a deploy](#running-a-deploy). It fetches the fork branch into a dedicated deploy clone and rebuilds every compose project when the branch moved.
3. When `GITOPS_CPA_MANIFEST` is set, each run then applies [`cpa-converge.py`](cpa-converge.py) to the CPA core and native plugins; see [CPA native plugins](#cpa-native-plugins).

The fork does not follow `router-for-me/CLIProxyAPI` automatically; upstream changes reach `master` only when merged by hand.

Nothing pushes to the host. The host pulls. To roll back, move `master` in the fork and run `pull-deploy.sh` again.

## CPA native plugins

[`cpa-plugins.json`](cpa-plugins.json) declares what the CPA container serves from its plugin directory: each plugin's committed `.so` artifact and each dashboard file. Deploying a plugin is committing its bookworm-built artifact to `master`; nothing else is needed.

On every tick `cpa-converge.py` compares each artifact in the deploy clone with the live copy by SHA-256 and changes only what differs:

| Kind | How it is applied |
| --- | --- |
| Dashboard file | Replaced atomically. The host reads it per request. |
| Plugin, `"reload": "hot"` | Installed as the only loadable `<id>-v0.1.0.<date>.g<commit>.so`, then re-enabled with `PATCH /v0/management/plugins/<id>/enabled`. |
| Plugin, `"reload": "restart"` | Swapped under `docker stop -t 240` / `docker start`, only after the host shows no Omni turn for 65 seconds: no unanswered Omni pick in the last 12 minutes of `main.log` and no account generating. A busy host leaves the swap to a later tick. |

The replaced file stays next to the new one as `<old name>.prev-g<commit>`, which the host does not load. A plugin the host does not report as loaded within the health wait gets one `docker restart`; if it still does not load, the tick fails and nothing is rolled back. Compose is never used for the CPA container: its image cannot be pulled on the host, and a compose run that fails to pull takes the container off its network.

Configuration, all optional:

| Variable | Default |
| --- | --- |
| `CPA_PLUGIN_DIR` | `/opt/dashboard/plugins` |
| `CPA_CONTAINER` | `cliproxyapi` |
| `CPA_API` | `http://127.0.0.1:18318` |
| `CPA_CORE_ENV` | `/etc/cliproxy/gemini-web-local/core.env` (read for `MANAGEMENT_PASSWORD`) |
| `CPA_CORE_BINARY` | `/opt/dashboard/gemini-web-local/CLIProxyAPI` |
| `CPA_DRY_RUN` | unset; `1` prints the plan and changes nothing |

Check a host before enabling it:

```bash
cd /opt/cliproxy && CPA_DRY_RUN=1 python3 deploy/cpa-converge.py deploy/cpa-plugins.json
```

Tests: `cd deploy && python3 -m unittest cpa_converge_test`.

## CPA core

The container bind-mounts the core executable from `/opt/dashboard/gemini-web-local/CLIProxyAPI`. `master` decides its version too, but the executable is too large to commit, so [`core-build`](../.github/workflows/core-build.yml) builds it and `cpa-converge.py` installs the build.

The `core` entry of [`cpa-plugins.json`](cpa-plugins.json) names the core paths, the release the builds are published on, and the asset prefix. The core commit is the last first-parent `master` commit that touched those paths; the workflow and the converger resolve it with the same `git log` query.

- `core-build` runs on pushes to `master` that touch the core paths, and by hand. For a core commit without a published build, it runs `go test ./...` and builds `./cmd/server` with cgo on `golang:1.26-bookworm`, the container's Debian 12 base, with the commit stamped into the build. It publishes `CLIProxyAPI-<commit>` and `CLIProxyAPI-<commit>.sha256` on the `cpa-core` prerelease and keeps the newest ten builds. A commit whose tests fail is never published.
- `cpa-converge.py` compares the published SHA-256 of that build with the live executable; builds are not byte-reproducible, so the published build decides. When they differ, it downloads and verifies the build before anything stops, then swaps it in the same idle-gated `docker stop -t 240` / `docker start` window as the restart plugins, so a core change and a plugin change share one restart. The host must then report the new commit in `X-CPA-COMMIT`, every manifest plugin loaded, and as many ready accounts as before. Otherwise it gets one `docker restart`, and the run fails without a rollback. The replaced executable stays as `CLIProxyAPI.prev`.
- A core commit whose build is not published yet fails the run before anything changes. Run `pull-deploy.sh` again after `core-build` finishes.

## One-time host setup

```bash
git clone --branch master https://github.com/jclee941/CLIProxyAPI.git /srv/cliproxy
cd /srv/cliproxy
chmod +x deploy/pull-deploy.sh

cp deploy/telegram-log-forwarder/.env.example deploy/telegram-log-forwarder/.env
cp deploy/telegram-usage-bot/.env.example deploy/telegram-usage-bot/.env
chmod 600 deploy/telegram-log-forwarder/.env deploy/telegram-usage-bot/.env
```

Fill in the secrets in each `.env`. Both files are git-ignored, so the converger's clean-tree check ignores them and they survive every `git reset --hard`.

The deploy clone is reset with `git reset --hard` on every update. Use it only for deployment; never edit code inside it. The converger aborts if `git status --porcelain` is non-empty.

## Running a deploy

There is no cron entry; deploys are explicit. After `master` moves, and after `core-build` has published any core change, run the converger on the host:

```bash
GITOPS_DEPLOY_DIR=/srv/cliproxy GITOPS_CPA_MANIFEST=deploy/cpa-plugins.json /srv/cliproxy/deploy/pull-deploy.sh >> /srv/cliproxy/pull-deploy.log 2>&1
```

A run that finds an Omni turn in flight leaves its restart to the next run. Overlapping runs are safe: the script takes a non-blocking `flock` on a lock file next to the deploy directory and skips the run if another one is active.

### Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `GITOPS_DEPLOY_DIR` | *(required)* | Dedicated deploy clone |
| `GITOPS_REMOTE` | `https://github.com/jclee941/CLIProxyAPI.git` | Git remote to fetch |
| `GITOPS_BRANCH` | `master` | Branch to converge on |
| `GITOPS_COMPOSE_FILES` | `docker-compose.yml deploy/telegram-usage-bot/docker-compose.yml` | Space-separated compose files, applied in order |
| `GITOPS_LOCK_FILE` | `<deploy dir>.pull-deploy.lock` | Lock file path |
| `GITOPS_CPA_MANIFEST` | *(unset)* | CPA manifest relative to the deploy clone, e.g. `deploy/cpa-plugins.json`; enables `cpa-converge.py` on every run |

`pull-deploy.log` is not rotated by the script. Add a `logrotate` entry if it grows.
