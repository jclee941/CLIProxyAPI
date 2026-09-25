# Elastic Stack logging

CLIProxyAPI can emit structured JSON logs that the bundled Logstash pipeline
indexes in Elasticsearch and exposes through Kibana.

Error-level events can also be sent to Telegram by setting
`TELEGRAM_BOT_TOKEN` and `TELEGRAM_CHAT_ID`. Docker Compose forwards both
variables to CLIProxyAPI.

## Start the stack

Copy `config.example.yaml` to `config.yaml` and enable file-backed JSON logs:

```yaml
logging-to-file: true
log-format: "json"
```

Then start CLIProxyAPI with the Elastic Stack overlay:

```bash
docker compose -f docker-compose.yml -f docker-compose.elk.yml up -d --build
```

Supply actual Telegram values through a protected local environment file,
kept outside the repository with mode `0600`:

```bash
docker compose --env-file /etc/cliproxy/telegram.env \
  -f docker-compose.yml -f docker-compose.elk.yml up -d --build
```

No external secret CLI is required. Never commit the environment file or print
its values while checking the deployment.

The default local endpoints are:

- CLIProxyAPI: `http://localhost:8317`
- Elasticsearch: `http://localhost:9200`
- Kibana: `http://localhost:5601`

Logstash tails the same `${CLI_PROXY_LOG_PATH:-./logs}` directory mounted by
CLIProxyAPI and writes daily `cliproxy-YYYY.MM.dd` indices.

## Verify ingestion

After CLIProxyAPI has emitted a log entry, check the index and retrieve the
latest event:

```bash
curl -fsS 'http://localhost:9200/_cat/indices/cliproxy-*?v'
curl -fsS 'http://localhost:9200/cliproxy-*/_search?size=1&sort=@timestamp:desc'
```

In Kibana, create a data view named `cliproxy-*` and select `@timestamp` as its
time field. Structured events include `service.name`, `event.dataset`,
`log.level`, `http.request.id`, `error.message`, and allowlisted application
fields under `fields`.

## Configuration

The stack version and local ports can be overridden without editing Compose:

```bash
ELASTIC_STACK_VERSION=9.4.3 ELASTICSEARCH_PORT=19200 KIBANA_PORT=15601 \
  docker compose -f docker-compose.yml -f docker-compose.elk.yml up -d
```

Heap sizes default to 512 MiB for Elasticsearch and 256 MiB for Logstash. Use
`ELASTICSEARCH_JAVA_OPTS` and `LOGSTASH_JAVA_OPTS` to override them.

This overlay disables Elastic security and binds Elasticsearch and Kibana to
localhost, so it is intended for local or otherwise trusted hosts. Enable
Elastic authentication and TLS before exposing either service on a network.
