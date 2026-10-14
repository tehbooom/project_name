# Project Name Generator

[![Go Report Card](https://goreportcard.com/badge/github.com/tehbooom/project_name)](https://goreportcard.com/report/github.com/tehbooom/project_name)

An HTTP API that generates random project names in the form `noun-adjective`.

## Run locally

```bash
go run .
```

The server listens on port `8080` by default. Set `LISTENING_PORT` or
`WORDS_DIR` to override the port or word-file directory.

## Containers

Build and run the application with PostgreSQL:

```bash
docker compose -f examples/static-postgres/compose.yaml up --build
```

The development stack automatically applies the PostgreSQL schema, imports the
seed vocabulary, and creates a temporary local administrator token. Read it
with:

```bash
docker compose -f examples/static-postgres/compose.yaml \
  exec app cat /tmp/project-name-admin-token
```

Stop the stack with
`docker compose -f examples/static-postgres/compose.yaml down`. Add `--volumes`
to also remove the PostgreSQL data. The included image runs as a non-root user
with a read-only root filesystem in Compose.

Additional runnable stacks are documented in [`examples/README.md`](examples/README.md):

- Static administrator with memory storage
- Static administrator with durable file storage
- PostgreSQL with Keycloak OIDC
- Elastic Stack with EDOT Collector and OTLP traces, metrics, and logs

## Quality checks

Run lint and race-enabled unit tests:

```bash
make lint
make unit
```

Run black-box E2E tests against the lightweight Compose stacks:

```bash
make e2e
```

Individual stacks can be selected:

```bash
./scripts/e2e.sh static-memory
./scripts/e2e.sh static-file
./scripts/e2e.sh static-postgres
./scripts/e2e.sh postgres-keycloak
./scripts/e2e.sh elastic-otel
```

E2E tests cover health, public generation, authentication, contributor
authorization, proposals, duplicate rejection, moderator listing, approval,
and idempotent deletion. Firestore is excluded because there is no Firestore
service in the Compose examples. GitHub Actions runs lint, unit tests, and each
Compose E2E stack in a separate job.

## Health checks

```text
GET /health/live   process is running
GET /health/ready  storage is reachable and approved words are available
```

Liveness never checks external dependencies. Readiness has a two-second
deadline and returns `503` while the configured word store is unavailable.
Health responses disable caching and are excluded from HTTP tracing.

## Web interface

Open [http://localhost:8080](http://localhost:8080) for the responsive project
name generator and moderator studio. The frontend is a standalone React,
TypeScript, Motion, and Vite application served by an unprivileged Nginx
container. Nginx proxies `/v1`, `/health`, `/project`, and `/otel` to the
internal Go service, which is not published directly by the Compose examples.

The studio accepts either an OIDC/Firebase bearer token or the local static
administrator token. Credentials are kept only in JavaScript memory and are
cleared on refresh or disconnect. For the default Compose stack, retrieve the
local token with:

```bash
docker compose -f examples/static-postgres/compose.yaml \
  exec app cat /tmp/project-name-admin-token
```

The Elastic example also enables browser OpenTelemetry. It exports page-load,
fetch, interaction, JavaScript-error, and Web Vitals telemetry through bounded
same-origin `/otel/v1/*` endpoints; browsers never connect directly to the
collector. Browser telemetry is disabled unless
`BROWSER_OTEL_EXPORTER_OTLP_ENDPOINT` is configured. Set
`BROWSER_OTEL_SAMPLE_RATIO` between `0` and `1` (`0.1` is the default).

For local frontend development:

```bash
cd frontend
npm install
npm run dev
```

Vite proxies API and telemetry traffic to a Go service on port `8080`.
`make web` produces the production frontend bundle.

## Generate a name

```bash
curl http://localhost:8080/v1/names/random
```

```json
{
  "name": "gopher-curious"
}
```

Optionally cap each generated word by character count. `max_length` applies to
both categories, while category-specific values override it:

```bash
curl 'http://localhost:8080/v1/names/random?max_length=12&max_noun_length=8'
```

Accepted limits are `1` through `64`; omitted limits are unrestricted.

Generation is safe by default. Unsafe-only generation must be enabled by the
operator and explicitly requested:

```bash
export UNSAFE_MODE_ENABLED=true # false when omitted
curl 'http://localhost:8080/v1/names/random?safe=false'
```

The public `GET /v1/config` response tells the frontend whether the optional
unsafe mode is available. Enabling the feature never changes the safe default.

The legacy `GET /project` route remains available, but its old public POST and
DELETE operations have been removed.

## Word moderation

Contributors can propose words, but proposals are not used for generation until
a moderator approves them:

```bash
curl \
  -X POST \
  -H "Authorization: Bearer $ID_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"category":"nouns","value":"capybara"}' \
  http://localhost:8080/v1/words
```

Moderators can list proposals and then approve or delete them:

```bash
curl \
  -H "Authorization: Bearer $ID_TOKEN" \
  "http://localhost:8080/v1/words?category=nouns&status=proposed&limit=50"

curl -X POST \
  -H "Authorization: Bearer $ID_TOKEN" \
  "http://localhost:8080/v1/words/WORD_ID/approve"

curl -X PATCH \
  -H "Authorization: Bearer $ID_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"safe":false}' \
  "http://localhost:8080/v1/words/WORD_ID/safety"

curl -X DELETE \
  -H "Authorization: Bearer $ID_TOKEN" \
  "http://localhost:8080/v1/words/WORD_ID"
```

The `contributor` role can create proposals. New words are safe by default. The
`moderator` role can create, list, approve, classify, and delete words.

## Moderator word listing

The word-list endpoint is protected and cursor-paginated:

```bash
curl \
  -H "Authorization: Bearer $FIREBASE_ID_TOKEN" \
  "http://localhost:8080/v1/words?category=nouns&limit=50"
```

Pass the returned `next_page_token` as `page_token` to request the next page.
The default page size is 50 and the maximum is 200.

## Google Identity Platform

Protected routes fail closed when no authentication driver is configured. To
validate Firebase/Google Identity Platform ID tokens:

```bash
export AUTH_DRIVERS=google-identity-platform
export FIREBASE_PROJECT_ID=your-project-id
export GOOGLE_APPLICATION_CREDENTIALS=/path/to/service-account.json # local only
go run .
```

On Google Cloud, Application Default Credentials come from the workload
identity, so a service-account key file is not required. A user is a moderator
when their verified ID token contains either `"moderator": true` or a
`"moderator"` entry in its `roles` claim. Contributors use the corresponding
`contributor` boolean or role. Browser sign-in and provider selection are
handled by the configured Identity Platform client; this API accepts the
resulting ID token.

## Generic OIDC and JWKS

Generic OIDC discovery and direct JWKS validation are configured with JSON.
Each provider must define an issuer, audience, and role claim:

```bash
export AUTH_DRIVERS=oidc
export OIDC_PROVIDERS_JSON='[
  {
    "name": "keycloak",
    "issuer": "https://identity.example.com/realms/project-name",
    "audience": "project-name",
    "role_claim": "realm_access.roles",
    "moderator_role": "moderator"
  }
]'
go run .
```

Set `jwks_url` when the issuer does not support OIDC discovery. Accepted
algorithms default to RS256, ES256, PS256, and EdDSA; symmetric JWT algorithms
are intentionally rejected.

## Local static administrator

The static-token driver creates a new 256-bit token on every startup and writes
it to a file readable only by its owner:

```bash
export AUTH_DRIVERS=static-token
export STATIC_ADMIN_TOKEN_FILE=/tmp/project-name-admin-token
go run .

curl \
  -H "X-Admin-Token: $(cat /tmp/project-name-admin-token)" \
  "http://localhost:8080/v1/words?category=nouns&limit=50"
```

This driver is intended for local, single-instance use. Do not enable it as a
fallback when another provider fails. Multiple explicit drivers can be enabled
with a comma-separated value such as `AUTH_DRIVERS=firebase,oidc`.

## Word storage

The default in-memory driver loads the seed files from `WORDS_DIR`; mutations
last only for the lifetime of the process:

```bash
export WORD_STORE_DRIVER=memory
```

The file driver persists proposals, approvals, and deletions using atomic file
replacement:

```bash
export WORD_STORE_DRIVER=file
export WORD_STORE_FILE=/var/lib/project-name/words.json
go run .
```

When the state file does not exist, it is initialized from `WORDS_DIR`. The
file driver is safe for concurrent requests within one process, but it is not a
shared multi-instance database.

### PostgreSQL

Apply `internal/wordstore/migrations/001_words.sql` through the deployment
pipeline, then configure:

```bash
export WORD_STORE_DRIVER=postgres
export WORD_STORE_POSTGRES_DSN='postgres://user:password@host/database'
go run .
```

`WORD_STORE_AUTO_MIGRATE=true` applies the embedded migration at startup and is
intended for development. `WORD_STORE_SEED=true` idempotently imports the JSON
seed files; disable it after the initial import.

### Firestore

The optional Firestore deployment files live in `examples/firestore`. Deploy
the required composite indexes from the repository root:

```bash
firebase deploy \
  --config examples/firestore/firebase.json \
  --project your-project-id \
  --only firestore:indexes
```

Then configure the driver:

```bash
export WORD_STORE_DRIVER=firestore
export WORD_STORE_FIRESTORE_PROJECT_ID=your-project-id
export WORD_STORE_FIRESTORE_COLLECTION=words
go run .
```

Application Default Credentials are used. For the local emulator, also set
`FIRESTORE_EMULATOR_HOST`. As with PostgreSQL, `WORD_STORE_SEED=true` performs
an idempotent initial import; importing the full bundled vocabulary performs
thousands of writes and should not run on every startup.
