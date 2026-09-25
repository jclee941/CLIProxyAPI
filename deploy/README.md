# Deployment

Sidecars and the GitOps convergence loop for this CLIProxyAPI fork.

- [telegram-log-forwarder](telegram-log-forwarder/README.md) — forwards new `error-*.log` entries to Telegram.
- [telegram-usage-bot](telegram-usage-bot/README.md) — Telegram bot that reports CLIProxyAPI usage.

## GitOps model

The fork's `main` branch is the single source of truth for what runs on the host.

1. The [`upstream-sync`](../.github/workflows/upstream-sync.yml) workflow merges `router-for-me/CLIProxyAPI@main` into this fork's `main` every 30 minutes. It only runs on forks. On a merge conflict it aborts the merge and opens or updates one persistent issue labelled `upstream-sync`, then fails the run.
2. The host converger [`pull-deploy.sh`](pull-deploy.sh) runs from cron, fetches the fork branch into a dedicated deploy clone, and rebuilds every compose project when the branch moved.
3. When `GITOPS_CPA_MANIFEST` is set, every tick then runs [`cpa-converge.py`](cpa-converge.py) against the CPA native plugins; see [CPA native plugins](#cpa-native-plugins).

Nothing pushes to the host. The host pulls. To roll back, move `main` in the fork and wait for the next tick.

## CPA native plugins

[`cpa-plugins.json`](cpa-plugins.json) declares what the CPA container serves from its plugin directory: each plugin's committed `.so` artifact and each dashboard file. Deploying a plugin is committing its bookworm-built artifact to `main`; nothing else is needed.

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
| `CPA_DRY_RUN` | unset; `1` prints the plan and changes nothing |

Check a host before enabling it:

```bash
cd /opt/cliproxy && CPA_DRY_RUN=1 python3 deploy/cpa-converge.py deploy/cpa-plugins.json
```

Tests: `cd deploy && python3 -m unittest cpa_converge_test`.

## One-time host setup

```bash
git clone --branch main https://github.com/jclee941/CLIProxyAPI.git /srv/cliproxy
cd /srv/cliproxy
chmod +x deploy/pull-deploy.sh

cp deploy/telegram-log-forwarder/.env.example deploy/telegram-log-forwarder/.env
cp deploy/telegram-usage-bot/.env.example deploy/telegram-usage-bot/.env
chmod 600 deploy/telegram-log-forwarder/.env deploy/telegram-usage-bot/.env
```

Fill in the secrets in each `.env`. Both files are git-ignored, so the converger's clean-tree check ignores them and they survive every `git reset --hard`.

The deploy clone is reset with `git reset --hard` on every update. Use it only for deployment; never edit code inside it. The converger aborts if `git status --porcelain` is non-empty.

## Cron

```cron
*/5 * * * * GITOPS_DEPLOY_DIR=/srv/cliproxy /srv/cliproxy/deploy/pull-deploy.sh >> /srv/cliproxy/pull-deploy.log 2>&1
```

Install it with `crontab -e`. Overlapping ticks are safe: the script takes a non-blocking `flock` on a lock file next to the deploy directory and skips the tick if another run is active.

### Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `GITOPS_DEPLOY_DIR` | *(required)* | Dedicated deploy clone |
| `GITOPS_REMOTE` | `https://github.com/jclee941/CLIProxyAPI.git` | Git remote to fetch |
| `GITOPS_BRANCH` | `main` | Branch to converge on |
| `GITOPS_COMPOSE_FILES` | `docker-compose.yml deploy/telegram-usage-bot/docker-compose.yml` | Space-separated compose files, applied in order |
| `GITOPS_LOCK_FILE` | `<deploy dir>.pull-deploy.lock` | Lock file path |
| `GITOPS_CPA_MANIFEST` | *(unset)* | CPA plugin manifest relative to the deploy clone, e.g. `deploy/cpa-plugins.json`; enables `cpa-converge.py` on every tick |

`pull-deploy.log` is not rotated by the script. Add a `logrotate` entry if it grows.

### Remove the cron entry

```bash
crontab -e   # delete the pull-deploy.sh line, then save
crontab -l   # confirm it is gone
```

Removing the cron entry stops future convergence only. Containers keep running; stop them with `docker compose -f <file> down` per project.
