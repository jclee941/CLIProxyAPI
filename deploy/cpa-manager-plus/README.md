# CPA Manager Plus v1.12.11 Runbook

Status as of 2026-09-11: **Manager deployed and runtime-verified on
192.168.50.114**. HTTP collection is running against the existing CPA gateway.
Production CPA is **Plus v7.2.157-4-webplugins.1 on port 8317**, with its existing
configuration preserved after compatibility fixes. The Gemini Web plugin and
resource page are loaded; account activation and final integration verification
remain separate gates. This runbook covers the verified Manager increment.

## Deployment

| Resource | Deployed value |
| --- | --- |
| Source | `seakee/CPA-Manager-Plus`, MIT, tag `v1.12.11` |
| Commit | `e1a8788ab796f4d001c5d1e9851c418989b05424` |
| Image | `seakee/cpa-manager-plus:v1.12.11`, pinned to the OCI index digest in `compose.yml` |
| Manager HTTP/UI | `127.0.0.1:18317` and `192.168.50.114:18317`, `/management.html` |
| CPA upstream | `http://cliproxyapi:8317`, not Manager's port |
| Network | Existing external `cliproxyapi_backend` |
| SQLite | Named volume `cliproxy-cpa-manager-plus-data`, `/data/usage.sqlite` |
| Installed package | `/opt/cpa-manager-plus` |
| Lifecycle | Enabled `cpa-manager-plus.service`, using `start.sh` for start and reload |
| Compose activation | Explicit `--profile approval-required`, `pull_policy: never` |

Manager is a separate Compose project. Don't run `down` on the existing CPA
project or replace `/usr/local/sbin/cliproxy-compose`. That wrapper loads
`/etc/cliproxy/op-service-account.env`, injects `/opt/dashboard/config.local.yaml`
into `/run/cliproxy/config.yaml`, and combines the dashboard-only, chatgpt2api,
Gemini Web plugin, and Plus image Compose files. It also preserves chatgpt2api
runtime injection. Preserve CPA, Postgres,
Telegram containers, storage, settings, and behavior.

LAN access is enabled; this isn't a loopback-only deployment. Public publication
and reverse-proxy/TLS changes require separate review. The container has a
read-only root filesystem, writable `/tmp` tmpfs, and no Docker socket. The
upstream image runs as root; Docker-root can still read mounted keys.

## Keys And Service Operations

Keep these three credentials distinct. Only references belong in this package:

| Purpose | Existing 1Password reference |
| --- | --- |
| Manager admin key | `op://homelab/kegtmfqwkjfunznum7bsi2o2di/password` |
| SQLite encryption key | `op://homelab/ob4zd62uo5xwvccd4gvlxzwhre/password` |
| CPA management key | `op://homelab/hazdy6l5k5rfahjdcdszzenizm/management_key` |

The key templates use `{{ op://vault/item/field }}` expressions, not bare
references. `start.sh` loads the existing service-account environment with
`umask 077`, injects both keys into `.next` files under
`/run/cliproxy-manager-plus`, applies mode `600`, checks both are nonempty, then
renames them into place. It force-recreates only Manager with the explicit
Compose profile so bind mounts pick up the new inodes.

The admin file is mounted read-only at `/run/secrets/cpa_admin_key`; the data key
is mounted read-only at `/data/data.key`. The entrypoint refuses empty/missing
keys. The enabled systemd unit provisions runtime keys on boot; Docker's restart
policy alone can't restore `/run` files. A service reload and container recreation
were verified, not a host reboot.

For routine operations on .114:

```sh
systemctl status cpa-manager-plus.service
systemctl reload cpa-manager-plus.service
```

Reload re-injects keys and recreates Manager. Don't recreate 1Password items for
routine starts. Never print injected config, Docker `Config.Env`, credential
payloads, or keys; don't use shell tracing, `--reveal`, or `--dry-run` around
secrets. Resolve references through `op run` for API calls, constructing headers
and JSON in memory rather than substituting secret text into JSON.

Changing the admin file **doesn't rotate an existing SQLite admin hash**. Use
the upstream offline `reset-admin-key --admin-key-file` workflow under separate
approval. Never reset the database to recover an admin key. The data key uses
the accepted 32-byte format and must never rotate independently of SQLite.

## Setup And Collection

Setup is already complete. Don't rerun fresh initialization as a health check.
`setup.template.json` records the safe initial body with both
`requestMonitoringEnabled:false` and `ensureUsageStatisticsEnabled:false`.
The CPA key reference must be resolved in memory before submission. Initial setup
validated CPA and persisted its connection encrypted in SQLite with collection
stopped; collection was enabled in a later step.

All protected requests use `Authorization: Bearer <Manager admin key>`, not the
CPA key. Writes use `Content-Type: application/json`.

| Request | Acceptance check |
| --- | --- |
| `GET /health` | Public 200; liveness only |
| `GET /usage-service/info` | Public readiness/setup metadata |
| `GET /usage-service/config` | `source="db"`, `managementKeyConfigured=true`, collector enabled; key redacted |
| `POST /v0/management/cpa-connection/validate` | Empty body; `configured=true`, `source="db"` |
| `GET /usage-service/account-processing-policy` | Cooldown/account-action automation disabled and locked |
| `GET /status` | Authenticated SQLite event count and running HTTP collector; unauthenticated request returns 401 |

Collection was activated with authenticated `POST /setup`, retaining the setup
body but setting `requestMonitoringEnabled:true` and keeping
`ensureUsageStatisticsEnabled:false`. Parsed CPA configuration before and after
was equal. For any future activation, first verify usage statistics are already
enabled, queue retention is positive, and the 500 ms polling interval doesn't
exceed retention. Required CPA changes belong in the existing approved injection
workflow, not Manager's proxy.

Don't enable collection through `PUT /usage-service/config`: an enabled resulting
collector triggers CPA's `PUT /usage-statistics-enabled`. Don't supply the
environment-managed CPA upstream/key shortcut either. A setup HTTP 200 alone
isn't proof of collection because setup ignores a collector Start error. Check
`collector.collector="running"`, `transport="http"`, and a newly inserted event
after a request through CPA **8317**.

Manager is the HTTP queue collector. Never use `GET /usage-queue` as a read-only
health probe: it consumes events. Before adding or changing consumers, review
queue ownership. The Telegram source audit found no queue consumer; it reads
`/v0/management/api-key-usage` and `/v0/management/usage-statistics-enabled`.
The deployed Plus retains those contracts. This audit isn't
evidence of Telegram bot UI interaction. Don't point the bot at Manager as a
URL-only migration. See [EVIDENCE.md](EVIDENCE.md) for queue semantics.

Collection doesn't enable account deletion, cooldown, auto-disable, inspection,
or recovery automation. Those features remain off. Don't invoke account-action
mutation endpoints as acceptance probes or change CPA's canonical YAML or
management UI repository through Manager without separate review.

## Persistence, Privacy, And Rollback

- Manager settings, admin hash, encrypted CPA key, inspection state, and usage
  history persist in SQLite, not in the existing Postgres container. Preserve the
  entire Manager volume and the exact matching 1Password data-key item/version.
- The `/data/data.key` bind is **not contained in a bare named-volume backup**.
  A restore bundle must include the database snapshot, key reference/version,
  and recoverable encrypted backup of that key in 1Password. Never copy a live
  SQLite main file without WAL handling; use upstream snapshot tooling or an
  approved stopped/checkpointed backup. Don't upload raw SQLite or keys to logs.
- `CPAMP_UPDATE_CHECK_ENABLED=false` disables automatic release checks.
  `externalUsageService` remains false. Don't trigger price sync, update checks,
  OAuth probes, or plugin installation as routine health checks. This isn't a
  network-level egress deny policy on the shared backend network.
- On rollback, stop/remove only Manager, preserving its named volume and
  1Password items. Never use `down -v`, recreate Postgres, modify Telegram bot
  state, or restore Manager SQLite over CPA storage. Core rollback needs its own
  counter/connection continuity assessment.

## Verified Scope And Remaining Gates

The setup, collection, request, and restart receipts are indexed in
[EVIDENCE.md](EVIDENCE.md). A CPA chatgpt2api request using `gpt-5-6-t-mini`
returned `MANAGER_CHATGPT_OK`, and Manager inserted one event. After service
reload, SQLite still contained one event and the collector was running; the
process-local inserted counter had reset to zero.

Authenticated Dashboard and AI Providers browser checks were performed. The
provider screenshot shows ChatGPT2API enabled with 27 models. The original CPA
inventory was 58 models and 11 auth entries; these are distinct counts, not a
claim that ChatGPT2API contains all CPA models.

Manager acceptance alone does not prove that all Gemini Web sessions are usable.
Complete the account and model execution gates while preserving existing settings
before calling the wider work complete.
