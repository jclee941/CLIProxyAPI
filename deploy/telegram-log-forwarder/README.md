# Telegram Log Forwarder

This standalone sidecar watches CLIProxyAPI `error-*.log` request logs and sends new failures to Telegram. It does not replace, restart, reconfigure, or share a network namespace with the existing CLIProxyAPI container.

## Behavior

- Mounts `dashboard_cliproxyapi_logs` read-only.
- Treats existing error logs as the startup baseline and alerts only on new files.
- Persists delivered filenames under `./state` to prevent duplicates after restart.
- Groups matching errors for five minutes and shows their rolling 24-hour occurrence count.
- Redacts URL query strings and common token formats.
- Uses Telegram HTML formatting with severity, environment, instance, route, model, time, and a request-log dashboard link.

## Run

```bash
cp .env.example .env
chmod 600 .env
mkdir -p state
chown 65532:65532 state
docker compose up -d --build
```

Set `TELEGRAM_BOT_TOKEN` and `TELEGRAM_CHAT_ID` in `.env` before starting the service.
