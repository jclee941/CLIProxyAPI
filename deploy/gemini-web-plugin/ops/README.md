# Local Session Maintenance

The optional maintenance job calls the authenticated local CPA API using an
existing local management credential. It needs Python 3.10+ and the standard
library; no external secret CLI or sidecar is started by this job.

## Credentials

`run.sh` starts the runner, which reads `MANAGEMENT_PASSWORD` from
`/etc/cliproxy/gemini-web-local/core.env` as data, without executing shell code
or exporting the key. Preserve the existing key. The file must remain root-owned,
mode `0600`, in a protected directory. `GEMINI_MAINTENANCE_ENV_FILE` can override
the path for isolated checks.

Missing files or empty keys fail before HTTP. Never enable shell tracing, dump
the environment, or pass the credential on a command line.

## Runtime contract

`maintain.py` submits exactly one POST to
`http://127.0.0.1:8317/v0/management/plugins/gemini-web/maintain`.
The default body is `{}`; `--id gemini-web-<id>.json` selects one existing account.
The runner does not retry, follow redirects, use environment proxies, or set a
request timeout.

The response is bounded to 64 KiB. The runner validates IDs and states, then
prints counts only. It never prints response bodies, tokens, emails, or account
IDs. Exit 0 means a valid cycle report, not that every account is ready.
Exit 1 indicates HTTP, transport, or response failure; exit 2 indicates invalid
arguments or credentials.

## Scheduling and deployment

The oneshot service runs the same `run.sh` entry point as manual invocation.
`Restart=no`, `OnBootSec=30s`, and `OnUnitInactiveSec=5min` prevent overlap and
measure the interval from completion. `TimeoutStartSec=0` avoids terminating an
active credential write. Installation must preserve the timer's current enabled
or disabled state.

```sh
install -d -m 0755 /opt/gemini-web-plugin/ops
install -m 0644 deploy/gemini-web-plugin/ops/maintain.py /opt/gemini-web-plugin/ops/maintain.py
install -m 0755 deploy/gemini-web-plugin/ops/run.sh /opt/gemini-web-plugin/ops/run.sh
install -m 0644 deploy/gemini-web-plugin/ops/gemini-session-maintenance.service /etc/systemd/system/
install -m 0644 deploy/gemini-web-plugin/ops/gemini-session-maintenance.timer /etc/systemd/system/
systemctl daemon-reload
```

Only enable scheduling when maintenance ownership and account readiness have
been verified. Before manual credential replacement, stop future scheduling and
let active credential owners drain. Do not kill a running maintenance service,
delete state files, clear fences, or restore stale session data to bypass an
uncertain operation. Routine cookie rotation does not require a container restart.

## Verification

```sh
python3 -B -m unittest discover -s deploy/gemini-web-plugin/ops/tests -v
sh -n deploy/gemini-web-plugin/ops/run.sh
```

Tests use synthetic keys, fresh subprocesses, and loopback HTTP fixtures. The
launcher tests place a failing external-secret CLI on PATH and prove it is never
called. An isolated systemd root validates unit structure. A safe deployment
check can invoke `run.sh --help` without contacting Google or renewing credentials.
