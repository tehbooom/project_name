# Runnable examples

Run one example at a time because each publishes the API on
`http://localhost:8080`.

## Static administrator with memory storage

```bash
docker compose -f examples/static-memory/compose.yaml up --build
docker compose -f examples/static-memory/compose.yaml \
  exec app cat /tmp/project-name-admin-token
```

All word changes disappear when the container is recreated.

## Static administrator with file storage

```bash
docker compose -f examples/static-file/compose.yaml up --build
docker compose -f examples/static-file/compose.yaml \
  exec app cat /tmp/project-name-admin-token
```

Word changes persist in the `word-data` volume. Remove it with:

```bash
docker compose -f examples/static-file/compose.yaml down --volumes
```

## Static administrator with PostgreSQL storage

```bash
docker compose -f examples/static-postgres/compose.yaml up --build
docker compose -f examples/static-postgres/compose.yaml \
  exec app cat /tmp/project-name-admin-token
```

The stack automatically applies the schema and imports the seed vocabulary.
Word changes persist in the `postgres-data` volume.

## PostgreSQL with Keycloak OIDC

```bash
docker compose -f examples/postgres-keycloak/compose.yaml up --build
```

Services:

- API: `http://localhost:8080`
- Keycloak: `http://localhost:8081`
- Keycloak administration: `admin` / `admin`
- Example contributor: `contributor` / `contributor`
- Example moderator: `moderator` / `moderator`

The example enables password grants only to make command-line testing easy.
Obtain a contributor token:

```bash
CONTRIBUTOR_TOKEN="$(
  curl -fsS \
    -d client_id=project-name \
    -d grant_type=password \
    -d username=contributor \
    -d password=contributor \
    http://localhost:8081/realms/project-name/protocol/openid-connect/token |
  jq -r .access_token
)"
```

Create a proposal:

```bash
curl -i \
  -X POST \
  -H "Authorization: Bearer $CONTRIBUTOR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"category":"nouns","value":"capybara"}' \
  http://localhost:8080/v1/words
```

Obtain a moderator token:

```bash
MODERATOR_TOKEN="$(
  curl -fsS \
    -d client_id=project-name \
    -d grant_type=password \
    -d username=moderator \
    -d password=moderator \
    http://localhost:8081/realms/project-name/protocol/openid-connect/token |
  jq -r .access_token
)"
```

List proposals:

```bash
curl \
  -H "Authorization: Bearer $MODERATOR_TOKEN" \
  "http://localhost:8080/v1/words?category=nouns&status=proposed"
```

The users, passwords, automatic migrations, and direct password grant are for
local demonstration only. Do not copy them into a production deployment.

## Elastic Stack with EDOT Collector

This stack runs the application, PostgreSQL, Elasticsearch, Kibana, and EDOT
Collector through Elastic Agent's OpenTelemetry mode:

```bash
docker compose -f examples/elastic-otel/compose.yaml up --build --wait
```

Services:

- API: `http://localhost:8080`
- Elasticsearch: `http://localhost:9200`
- Kibana: `http://localhost:5601`
- OTLP/gRPC: `localhost:4317`
- OTLP/HTTP: `localhost:4318`
- Elastic login: `elastic` / `elastic`

The application exports traces, metrics, and logs to EDOT over
OTLP/HTTP protobuf. EDOT applies Elastic trace processing and writes
OpenTelemetry-native data streams to Elasticsearch.

Generate telemetry:

```bash
curl http://localhost:8080/v1/names/random
curl http://localhost:8080/v1/names/random
```

Open the Kibana Observability UI:

```text
http://localhost:5601/app/apm/traces?rangeFrom=now-15m&rangeTo=now
```

Retrieve the local administrator token when testing protected operations:

```bash
docker compose -f examples/elastic-otel/compose.yaml \
  exec app cat /tmp/project-name-admin-token
```

Run its complete API and telemetry E2E check:

```bash
./scripts/e2e.sh elastic-otel
```

The E2E check waits for documents to appear in `traces-*`, `metrics-*`, and
`logs-*`. This stack is intentionally development-only: credentials are fixed,
TLS is disabled, and the Elastic trial license is enabled.
