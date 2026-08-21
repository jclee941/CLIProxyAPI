# Telegram Usage Bot

This standalone sidecar exposes CLIProxyAPI usage data through a Telegram bot. It reads the CLIProxyAPI management API over HTTP and does not replace, restart, reconfigure, or share a network namespace with the existing CLIProxyAPI container.

## Behavior

- Long-polls Telegram `getUpdates` and answers only chats listed in `TELEGRAM_ALLOWED_CHAT_IDS`.
- Replies with an inline keyboard; each button maps to a CLIProxyAPI management API read.
- Sends one scheduled usage report per day at `REPORT_TIME` when `REPORT_ENABLED` is `true`.
- Persists its state (last processed update offset, last report date) at `STATE_PATH` under `./state` so restarts do not replay updates or duplicate the daily report.
- Runs read-only, as UID 65532, with all capabilities dropped.

## Setup

```bash
cp .env.example .env
chmod 600 .env
mkdir -p state
chown 65532:65532 state
docker compose up -d --build
```

Fill `TELEGRAM_BOT_TOKEN`, `TELEGRAM_ALLOWED_CHAT_IDS`, and `CPA_MANAGEMENT_KEY` in `.env` before starting the service. `.env` is git-ignored and must never be committed.

`CPA_BASE_URL` defaults to `http://host.docker.internal:8317`. The compose file maps `host.docker.internal` to the host gateway, so this works on Linux hosts where CPA publishes port 8317. If CPA is bound to loopback only, use `http://127.0.0.1:8317` together with `network_mode: host`; that mode disables the compose bridge network and the `extra_hosts` entry, so apply it only when the default cannot reach CPA.

## Logs

```bash
docker compose logs -f telegram-usage-bot
```

## Update

```bash
git pull
docker compose up -d --build
```

On the deployment host this happens automatically: see [../README.md](../README.md) for the GitOps converger that pulls the fork and rebuilds every compose project.

## Buttons and CPA data

| Summary | Aggregate success/failure counters and recent-window traffic from `/v0/management/api-key-usage` |
| Providers | Per-provider and per-credential breakdown of the same endpoint, API keys masked to the last four characters |
| Failures | Only the credentials with failed requests, including recent-window failures |
| Status | Management API reachability plus the `usage-statistics-enabled` flag from `/v0/management/usage-statistics-enabled` |

Every button is a read. The bot never writes configuration, never rotates credentials, and never restarts CPA.

## Security notes

- `CPA_MANAGEMENT_KEY` is a full-privilege management credential. Keep `.env` at mode `600`, keep it out of git, and rotate the key if it is ever exposed in a log or a chat.
- Expose CPA's management port to the Docker host only. Do not publish it on a public interface just to satisfy `CPA_BASE_URL`.
- `TELEGRAM_ALLOWED_CHAT_IDS` is the only authorization boundary on the Telegram side. An empty allowlist must be treated as "no one is allowed", not "everyone is allowed".
- The bot token grants full control of the bot. Revoke it through @BotFather if a host is compromised.
- The container is `read_only` with `cap_drop: ALL` and `no-new-privileges`; `./state` is the only writable path and holds no secrets.
