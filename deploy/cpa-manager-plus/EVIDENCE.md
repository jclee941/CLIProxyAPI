# Manager v1.12.11 Review Evidence

Inspection date: 2026-09-11. The preparation sections below record the initial
read-only phase, when no production write was performed. The appended deployment
phase supersedes their pending-state claims without changing that historical
record.

## Provenance

- [Release v1.12.11](https://github.com/seakee/CPA-Manager-Plus/releases/tag/v1.12.11)
  exists, published `2026-09-10T11:32:37Z`.
- GitHub commit lookup resolves tag to `e1a8788ab796f4d001c5d1e9851c418989b05424`.
- [LICENSE at tag](https://github.com/seakee/CPA-Manager-Plus/blob/v1.12.11/LICENSE)
  is MIT, copyright 2026 Seakee.
- `docker buildx imagetools inspect seakee/cpa-manager-plus:v1.12.11` read only
  registry metadata; it did not pull image layers or start containers.
- OCI index: `sha256:a2e8c99c80f9193e42da02f468bbfd3eba1d865ff88954e1582d29bc98850a24`.
- Linux amd64 manifest: `sha256:3af6566ff8e822632dfa9932aacbc8ca8fe9b2cc012f944dbff0f53bd8450797`.
- Linux arm64 manifest: `sha256:f6b1b106f6546dd88df16874f98b67059f95efe0bffb85f98e595a6dbc6f5102`.
- `Dockerfile.manager-server` builds an embedded frontend and Go server, uses
  Alpine 3.21 with wget, entrypoint `cpa-manager-plus`, port 18317 and `/data`.
- Context7 lookup was attempted but its monthly quota was exceeded. The tagged
  source, release API, local 1Password CLI help, and registry metadata were used.

## Operational Metadata

Read-only SSH to `.114` confirmed:

| Container | Image | Relevant metadata |
| --- | --- | --- |
| `cliproxyapi` | `eceasy/cli-proxy-api:latest` | Publishes 8317 on loopback and 192.168.50.114 |
| `cliproxyapi-postgres` | `postgres:16-alpine` | Running, only container TCP 5432 |
| `cliproxy-telegram-usage-bot` | `cliproxy-telegram-usage-bot:current` | Running, entrypoint `/telegram-usage-bot`, null Cmd |
| `cliproxy-telegram-forwarder` | `cliproxy-telegram-log-forwarder:current` | Running, no host port |

Bot immutable image ID at inspection:
`sha256:608f83ee7cb2661a037dd289533423401e9462f576c645b97efa72855f50f5bb`.
It binds `/opt/cliproxy/deploy/telegram-usage-bot/state` to `/state` and has its
own network `cliproxy-telegram-usage-bot_default`; it is not being re-networked.
External `cliproxyapi_backend` exists and contains `cliproxyapi` and Postgres.
`ss -ltn '( sport = :8317 or sport = :18317 )'` showed only two 8317 listeners;
Docker port metadata showed no 18317 publication. Port availability is a snapshot.

The live wrapper source was read, but its referenced environment/config files
were not opened. It uses the paths and two Compose files documented in README.
No raw Docker Env, auth file, state data, service logs, or secret value was read.

## Queue Ownership Resolution

The **actual host source**, not just the local README, was inspected at
`/opt/cliproxy/cmd/telegram-usage-bot/{cpa.go,main.go,handlers.go}`. All seven
non-test Go files were searched for queue/service API and fetch symbols.

- `cpa.go:17`: `/v0/management/api-key-usage`.
- `cpa.go:18`: `/v0/management/usage-statistics-enabled`.
- `cpa.go:71,80`: the only CPA fetch methods use those constants.
- `handlers.go:75,82`: Status uses FetchStatus; Summary/Providers/Failures use
  FetchAPIKeyUsage. Commands are `/start`, `/usage`, `/help`.
- `main.go` constructs the same CPA client; no collector backend selection exists.
- No `usage-queue` or `usage-service` reference occurs in the production bot source.
- Source SHA256: `cpa.go` = `eca10231e5c3e8bff9a83f0d1fca169d09d8d06ee5cb0b8560818127add4b34c`;
  `main.go` = `959eb5118f7b5a70135553ff6d43ee36576674fa21dda102f32ca930c7704ab7`;
  `handlers.go` = `0bba1d6c897a9bbef1b6e2e16060f64d0d9e5ebcedab98c1f90fca9ee72c8cbe`.

Conclusion: **no destructive-consumer conflict with this bot; no bot change is
needed for Manager collection alone**. Host source/entrypoint identity is strong
evidence, not a reproducible source-to-running-binary attestation. Core Plus still
must pass the bot's API contract before replacement. Do not point `CPA_BASE_URL`
at Manager: Manager's router proxies `api-key-usage` back to CPA rather than
adapting its persisted stats into that schema. An incompatible Core Plus would
require a separately reviewed adapter/bot change, not a URL-only edit.

### Supported Alternatives If Another Consumer Is Found

The selected configuration explicitly uses HTTP and one Manager instance. Its
`internal/httpqueue/client.go:122` calls `GET /v0/management/usage-queue?count=100`.
The inspected local CPA `internal/api/handlers/management/usage.go:36` uses
`redisqueue.PopOldest`; this is destructive even though the HTTP verb is GET.
Local `api_key_usage.go:74` instead reads auth-manager snapshots, not that queue.

Manager v1.12.11 genuinely supports `collectorMode:"subscribe"`: its
`internal/collector/collector.go:160` authenticates a RESP connection and
subscribes to the usage channel. `auto` tries subscribe, HTTP, then RESP pop, so
`auto` is NOT safe when a non-destructive-only contract is needed.

The inspected local CPA `internal/redisqueue/queue.go:65,141` broadcasts to all
subscribers, but when any subscriber exists it **does not enqueue the event for
HTTP/RESP popping**. Subscriber buffers can overflow and disconnect; subscription
is not a durable replay/ack protocol. Therefore adding a subscriber alongside an
existing HTTP popper can starve that popper, not preserve it. Whether the selected
Core Plus has this exact behavior is owned by the independent Core Plus worker.

If another consumer is discovered, do not activate this HTTP collector. Supported
paths requiring separate review are: make Manager the sole collector and move
downstream consumers to its persisted `/v0/management/usage`/monitoring APIs with
an explicit schema adapter, or migrate ALL consumers to verified RESP subscribe
fan-out while accepting its transient delivery semantics. Do not claim that
arbitrary queue names, `auto`, or an extra Redis service creates durable fan-out.
No such adapter or consumer migration was implemented in this scope.

## Exact Tagged Source Contracts

All paths below are under `apps/manager-server/` in the pinned upstream tag.

| Source | Finding |
| --- | --- |
| `internal/config/config.go:144` | Admin env and `_FILE` supported; file secret is read at startup |
| `internal/service/bootstrap/service.go:104` | Existing admin hash wins; supplied key initializes only a fresh credential; generated key returned only when no supplied key |
| `cmd/cpa-manager-plus/main.go:127` | Only generated key is logged; avoids log disclosure with provided nonempty file |
| `internal/security/security.go:95,285` | File-path key accepts 32 bytes or base64 32 bytes; new file otherwise generated with mode 0600 |
| `internal/security/security.go:139` | CPA secret encryption via AES-GCM; store persists protected settings |
| `internal/http/router/router.go:51` | Public `/health`, `/usage-service/info`, protected `/setup` and config/status routes |
| `internal/service/setup/service.go:16,100` | Exact setup fields; validates CPA config, saves encrypted connection, starts/stops collector |
| `internal/service/setup/service.go:189,254` | Explicit false flags needed to avoid default collection/statistics enabling |
| `internal/service/managerconfig/service.go:112,144` | Config PUT merges fields but enabled collector triggers CPA usage-statistics PUT |
| `internal/service/managerconfig/service.go:338` | Config GET uses boolean `managementKeyConfigured`, not plaintext key |
| `internal/model/codex_inspection.go:221` | Inspection disabled, autoActionMode none, autoRecover false, xAI inference false |
| `internal/config/config.go:157` | Quota cooldown/account actions/auto-disable default false |
| `internal/service/automation/service.go:175` | Explicit environment settings lock automation fields over DB settings |
| `internal/app/context.go:156` | `CPAMP_UPDATE_CHECK_ENABLED=false` disables automatic release checks |
| `internal/service/updatecheck/service.go:327` | Automatic release checks otherwise run after startup and periodically |

These are deployable upstream capabilities, not modifications to Manager source.

## Checks Performed

Upstream tagged source was unpacked into
`/tmp/opencode/cpa-manager-plus-v1.12.11`; archive is
`/tmp/opencode/cpa-manager-plus-v1.12.11.tar.gz`.

Passed from `apps/manager-server`:

```sh
go test ./internal/config ./internal/security ./internal/service/bootstrap \
  ./internal/service/setup ./internal/service/managerconfig \
  ./internal/service/automation ./internal/httpapi
```

This exercises upstream setup disabling, admin authentication, encrypted storage,
configuration precedence, automation locks, and actual HTTP handler contracts.
It is not a production smoke test. Image execution, live .114 bootstrap, secret
reference resolution, and post-Core-Plus Telegram continuity remain approval
gates. Do not describe this preparation as a deployed or runtime-verified stack.

Additional observed checks:

- Docker Compose v5.0.2 renders the new manifest with exit code 0.
- `config --services` without the profile returns no enabled service; with
  `--profile approval-required` it returns only `cpa-manager-plus`. Profiles are
  an accidental-start guard, not an authorization boundary: explicitly targeting
  a profiled service can activate it, so operator approval still governs execution.
- Parsed normalized Compose JSON asserts loopback-only 18317, existing external
  backend, three mounts, no CPA credential/upstream environment shortcut,
  `pull_policy: never`, and all automation/update flags explicitly false.
- JSON template parsing and safety assertions pass. Both `.tpl` files contain
  only syntactically shaped placeholder `op://vault/item/field` references.
- All README shell blocks pass `sh -n`; the embedded bootstrap Python passes
  AST parsing. No credential-bearing snippet was executed.
- LSP reports no diagnostics for Compose, JSON, and both Markdown files. No
  language server is configured for `.tpl`; reference-format validation replaces
  that unsupported check. No LSP configuration was installed or changed.
- Tagged Manager `go build -o /tmp/opencode/cpa-manager-v1.12.11-build-check
  ./cmd/cpa-manager-plus` exits 0. Repository `go build -o
  /tmp/opencode/cpa-manager-packaging-repo-build-check ./cmd/server` also exits 0.
  These temporary binaries were not run or installed on .114.

## Deployment And Runtime Verification, 2026-09-11

This later phase deployed **Manager only**, pinned to v1.12.11 and the OCI index
digest above. It doesn't establish completion of the Core Plus replacement or
Gemini Web plugin integration. Deployment facts and test history below come from
the deployment handoff; the named JSON receipts and provider screenshot were
also read during the documentation update. No live checks were rerun for this
documentation-only change.

### Deployment And Key Provisioning

- Manager is installed under `/opt/cpa-manager-plus` on `.114`, publishing
  `127.0.0.1:18317` and `192.168.50.114:18317` on the same external
  `cliproxyapi_backend`. This supersedes the preparation snapshot of an unused,
  loopback-only planned port. Production CPA remains v7.2.157 on port 8317.
- The existing homelab references are
  `op://homelab/kegtmfqwkjfunznum7bsi2o2di/password` for Manager admin,
  `op://homelab/ob4zd62uo5xwvccd4gvlxzwhre/password` for the data key, and
  `op://homelab/hazdy6l5k5rfahjdcdszzenizm/management_key` for CPA. No secret
  values are recorded here.
- The earlier bare-reference template check was insufficient: `op inject` needs
  `{{ op://... }}` expressions to materialize keys. `tests/test_templates.py`
  first reported two failures, then passed after both templates were corrected.
  The current templates and regression assertion were inspected for this update;
  the red-to-green run is recorded from the deployment handoff, not rerun here.
- `start.sh` stages both runtime keys in `.next` files, applies mode `600`, checks
  both are nonempty, renames them, and force-recreates only Manager using the
  explicit `approval-required` Compose profile. The installed systemd unit is
  enabled for boot provisioning and uses this launcher for start and reload.
  Reload was exercised; a full host reboot wasn't part of the recorded check.

### Setup, Collection, And Persistence

| Phase | Observed result | Local receipt |
| --- | --- | --- |
| Initial authenticated setup | Healthy Manager; CPA connection configured from DB; collector stopped; automation locked off | `/tmp/opencode/manager-setup-ulw.json` |
| Collection activation | Parsed CPA config before/after equal; collector running over HTTP against `http://cliproxyapi:8317` | `/tmp/opencode/manager-collection-ulw.json` |
| CPA request collected | Running HTTP collector, `totalInserted=1`, no skipped events or dead letters | `/tmp/opencode/manager-after-chatgpt-ulw.json` |
| Service reload and container recreation | SQLite `events=1`; HTTP collector running; process-local `totalInserted=0` after restart | `/tmp/opencode/manager-after-restart-ulw.json` |

Initial setup verified Manager admin authentication and the encrypted persisted
CPA connection. An unauthenticated `/status` request returned 401. Collection
was then enabled through `POST /setup` with `requestMonitoringEnabled:true` and
`ensureUsageStatisticsEnabled:false`, not config PUT. The before/after parsed
CPA configuration comparison was equal, as recorded by
`cpa_configuration_preserved:true` in the collection receipt.

The actual CPA chatgpt2api request used `gpt-5-6-t-mini` and returned
`MANAGER_CHATGPT_OK`. The next receipt recorded one inserted event.
`systemctl reload cpa-manager-plus.service` then recreated Manager. The final
receipt's persistent event count of one, despite reset process counters, proves
SQLite event retention across that recreation. It doesn't prove backup restore
or data-key rotation; the paired SQLite/key backup warnings in the runbook still
apply.

### Browser Checks And Scope Limits

- Authenticated Dashboard and AI Providers checks were performed against the
  deployed Manager. Captures are `/tmp/opencode/manager-dashboard-ulw.png` and
  `/tmp/opencode/manager-chatgpt-provider-ulw.png`. The dashboard capture may show
  initial loading; no settled dashboard pixel claim is made here.
- The provider capture was read for this update. It visibly shows the enabled
  `chatgpt2api` provider at `http://chatgpt2api:80/v1` with 27 models. The original
  CPA inventory was 58 models and 11 auth entries, not 58 ChatGPT2API models.
- An initial checkbox automation attempt failed because of an overlay. Clicking
  the visible label fixed the test driver; no Manager source change was needed.
- The collector runs, but account deletion, quota cooldown, auto-disable, and
  related automation weren't enabled. Telegram evidence remains a source audit
  showing no queue consumer, not a claim of bot UI interaction.
- Core Plus replacement is still pending a regression fix. Gemini Web plugin/UI
  work is built but not yet public production. Existing provider visibility in
  Manager isn't proof of that new plugin integration. Both wider gates remain
  open; this evidence accepts only the Manager increment.
