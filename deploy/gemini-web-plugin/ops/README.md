# External Session Maintenance

The `.114` job is deployed with five validated host-only source bindings. Two
distinct scheduled runs completed five minutes apart with four ready accounts and
one intentionally disabled account. The public CPA model registry and a real
`gemini-web-flash-3.8` request were also verified after maintenance. This does not
promise to log an account back in when Google requires authentication.

Install the matching plugin/sidecar and source bindings before enabling the timer.
Preserve existing core, Manager, structured-output, ChatGPT2API and legacy Gemini
configuration. Every core rollout path must retain the read-only `op` binary mount
and the existing service-account environment, including paths that use only the
base Compose file. Missing runtime dependencies cause `secret_store_unavailable`,
not a Google cookie-expiration diagnosis.

## Runtime Contract

`maintain.py` requires Python 3.10+ and only the standard library. Each fresh
process reads `GEMINI_MAINTENANCE_KEY` from its environment and makes exactly one
POST to `http://127.0.0.1:8317/v0/management/plugins/gemini-web/maintain`.
The default body is `{}`; `--id gemini-web-synthetic-2.json` sends only that ID.
IDs must match `^gemini-web-[a-z0-9-]+\.json$` and be at most 128 characters.
The host's actual AuthID must be supplied for diagnostics, not this example.

There is no URL/key CLI option, `.env` loading, environment proxy, redirect,
request retry, fallback endpoint, worker pool, or secret stored on disk. The key
is used only in an in-process `Authorization: Bearer` header, never argv or a
URL. An unresolved `op://` value fails before HTTP. `op run` injects the key from
`op://homelab/hazdy6l5k5rfahjdcdszzenizm/management_key`; the existing protected
`/etc/cliproxy/op-service-account.env` provides the service-account environment.
Do not place `GEMINI_MAINTENANCE_KEY` in that file, which would override the unit's
reference; it must remain runtime-injected from 1Password. Never use `set -x`,
dump the environment, or interpolate the key into curl arguments/configuration.

The response is `{results:[{id,state,error?,next_due_at?}]}`, with no tokens.
The runner bounds it to 64 KiB and parses required IDs/states before any summary.
It prints one line of counts for the nine known states, including
`needs_operator`, and does not print account IDs, emails, arbitrary error text,
timestamps, or response bodies. Even an error matching `^[a-z0-9_]+$` is not
printed: a shape check alone is not an error-code allowlist. No per-account
warning spam or last-status file is needed. There is no StateDirectory or secret
cache; `UMask=0077` and core dumps disabled protect the service's incidental files.

Exit 0 means a valid cycle report, not that all accounts are ready. Account-level
failures and skips remain visible as counts without failing the unit repeatedly.
Exit 1 means HTTP/transport/response failure; exit 2 means invalid key or CLI.
Only fixed diagnostic codes and numeric HTTP status are printed on failure.
Consult the authenticated management API for per-account state when investigating;
do not enable manager HTTP body/header logging or copy raw diagnostic output into
journals. Explicit selection still respects backend fences/cooldowns/disabled state.

## Scheduling And Drain Safety

The oneshot service uses a fresh `op run -- python3` process for each invocation.
`Restart=no` means no failed-job restart. `OnBootSec=30s` schedules the initial
cycle and `OnUnitInactiveSec=5min` measures from completion, not start. systemd
does not start a second copy of this same unit while it is active. There is no
`Persistent=true`: catch-up persistence applies to calendar timers, not this
monotonic schedule. The regular next cycle is a new due-account evaluation,
not a replay of a failed request. Do not add cron, a second timer, parallel manual
runners, another plugin writer, or a plugin background scheduler.

`TimeoutStartSec=0` deliberately avoids killing a long batch mid-credential-write.
Five sequential accounts can take approximately 15 minutes plus 1Password writes;
the backend owns the per-credential 60-second worker budget and 70-second fence.
There is no runner HTTP timeout. A stalled backend can leave the unit active and
prevent future cycles, so investigate rather than blindly restarting it. These
settings do not prove that every remote operation is bounded on the target host.

Before external 1Password UI, CPA-core/ref edits, or manual token replacement:

1. Stop only future scheduling: `systemctl stop gemini-session-maintenance.timer`.
2. Stop admitting new Flash/Omni and manual credential writes. Let the active
   maintenance service and all in-flight credential owners drain normally.
3. Inspect `systemctl show gemini-session-maintenance.service -p ActiveState -p SubState`
   and the backend's credential state. An inactive client alone does not prove a
   disconnected backend writer has finished. A lost response or unknown write/
   submission needs operator reconciliation, not automatic retry or reset.
4. Make the intended controlled edit only after all writers drain. Expected-token
   comparison is not atomic CAS; stale 1Password item versions can overwrite.
5. Resume the single writer and timer only after resolving the uncertain state.

Do not use `systemctl stop/restart` on an active maintenance **service** as the
normal drain mechanism: terminating its HTTP client cannot safely cancel backend
credential writes. `host_sync_pending` preserves the newer 1Password token and
resynchronizes host state on the next cycle, with no rollback. Authentication
failure holds a 30-minute per-reference cooldown. Profile 1 stays disabled.
Routine account-cookie updates do not require a Docker restart.

## Local Checks

From the repository root, with Python 3.12+ for the typed test harness and
`systemd-analyze` installed (the production runner itself needs only 3.10+):

```sh
python3 -B -m unittest discover -s deploy/gemini-web-plugin/ops/tests -v
sh -n deploy/gemini-web-plugin/ops/run.sh
systemd-analyze verify deploy/gemini-web-plugin/ops/gemini-session-maintenance.service deploy/gemini-web-plugin/ops/gemini-session-maintenance.timer
```

Tests use ephemeral loopback HTTP servers and synthetic keys in fresh processes,
including stale `.env`, malformed responses, all states, invalid IDs, redirects,
and non-2xx failures. The systemd fixture check uses an isolated temporary root
with placeholder binaries, not the real service account, Docker, or host paths.
Missing fixed paths on a development machine are expected; a successful syntax
check is not proof that production Conditions or credentials are satisfied.

## Operator Installation And Fresh-Process Smoke

These commands are instructions to the integrating operator, not an automatic
deployment script. Run on `.114` after backend validation and the drain gate.
Use the approved existing deployment path to install the updated plugin/sidecar
and validated host-only `maintenance_sources`; do not modify core code or replace
the existing Compose wrapper/configuration. From the validated repository:

```sh
install -d -m 0755 /opt/gemini-web-plugin/ops
install -m 0644 deploy/gemini-web-plugin/ops/maintain.py /opt/gemini-web-plugin/ops/maintain.py
install -m 0755 deploy/gemini-web-plugin/ops/run.sh /opt/gemini-web-plugin/ops/run.sh
install -m 0644 deploy/gemini-web-plugin/ops/gemini-session-maintenance.service /etc/systemd/system/
install -m 0644 deploy/gemini-web-plugin/ops/gemini-session-maintenance.timer /etc/systemd/system/
systemd-analyze verify /etc/systemd/system/gemini-session-maintenance.service /etc/systemd/system/gemini-session-maintenance.timer
systemctl daemon-reload
systemctl start gemini-session-maintenance.service
systemctl show gemini-session-maintenance.service -p InvocationID -p Result -p ExecMainStatus -p ConditionResult
systemctl start gemini-session-maintenance.service
systemctl show gemini-session-maintenance.service -p InvocationID -p Result -p ExecMainStatus -p ConditionResult
journalctl -u gemini-session-maintenance.service -n 30 --no-pager
systemctl enable --now gemini-session-maintenance.timer
systemctl list-timers gemini-session-maintenance.timer --all
```

Keep the timer stopped during the two synchronous `start` commands; each must
finish before the next. Require distinct InvocationIDs, successful Conditions,
and two safe count summaries. Review backend authenticated account/status API
results, disabled Profile 1, per-reference cooldown/fencing, and reference-only
host auth without logging bodies or tokens. Verify Plus/Manager/ChatGPT routes
remain intact before enabling the timer. A successful HTTP response or `ready`
count alone is not proof of account entitlement or successful live deployment.

For a scoped diagnostic after stopping the timer and draining the service,
`/bin/sh /opt/gemini-web-plugin/ops/run.sh --id <existing-auth-id>` uses the same
reference and endpoint. It expects an already approved runtime service-account
environment; it never sources `.env` or the service-account file itself. Do not
run it alongside the unit. Prefer the service for ordinary smoke checks so the
existing EnvironmentFile is loaded by systemd, not copied into shell history.
