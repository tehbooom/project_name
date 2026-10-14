#!/usr/bin/env bash

set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

DEFAULT_CASES=(
  static-memory
  static-file
  static-postgres
  postgres-keycloak
  elastic-otel
)

if [[ $# -gt 0 ]]; then
  CASES=("$@")
else
  CASES=("${DEFAULT_CASES[@]}")
fi

compose_file() {
  case "$1" in
    static-memory) echo "examples/static-memory/compose.yaml" ;;
    static-file) echo "examples/static-file/compose.yaml" ;;
    static-postgres) echo "examples/static-postgres/compose.yaml" ;;
    postgres-keycloak) echo "examples/postgres-keycloak/compose.yaml" ;;
    elastic-otel) echo "examples/elastic-otel/compose.yaml" ;;
    *)
      echo "unknown E2E case: $1" >&2
      return 1
      ;;
  esac
}

wait_for_static_token() {
  local project="$1"
  local file="$2"
  local token=""
  for _ in $(seq 1 30); do
    token="$(
      docker compose -p "$project" -f "$file" \
        exec -T app cat /tmp/project-name-admin-token 2>/dev/null || true
    )"
    if [[ -n "$token" ]]; then
      printf '%s' "$token"
      return 0
    fi
    sleep 1
  done
  echo "static admin token was not created" >&2
  return 1
}

wait_for_keycloak_token() {
  local username="$1"
  local password="$2"
  local response=""
  local token=""
  for _ in $(seq 1 60); do
    response="$(
      curl -fsS \
        -d client_id=project-name \
        -d grant_type=password \
        -d "username=$username" \
        -d "password=$password" \
        http://localhost:8081/realms/project-name/protocol/openid-connect/token \
        2>/dev/null || true
    )"
    if [[ -n "$response" ]]; then
      token="$(
        python3 -c \
          'import json, sys; print(json.load(sys.stdin).get("access_token", ""))' \
          <<<"$response"
      )"
      if [[ -n "$token" ]]; then
        printf '%s' "$token"
        return 0
      fi
    fi
    sleep 1
  done
  echo "Keycloak did not issue a token for $username" >&2
  return 1
}

wait_for_elastic_signal() {
  local pattern="$1"
  local response=""
  local count="0"
  for _ in $(seq 1 60); do
    response="$(
      curl -fsS \
        -u elastic:elastic \
        "http://localhost:9200/${pattern}/_count?allow_no_indices=true" \
        2>/dev/null || true
    )"
    if [[ -n "$response" ]]; then
      count="$(
        python3 -c \
          'import json, sys; print(json.load(sys.stdin).get("count", 0))' \
          <<<"$response"
      )"
      if [[ "$count" -gt 0 ]]; then
        echo "    $pattern documents: $count"
        return 0
      fi
    fi
    sleep 1
  done
  echo "no documents appeared in $pattern" >&2
  return 1
}

wait_for_elastic_query() {
  local pattern="$1"
  local query="$2"
  local description="$3"
  local response=""
  local count="0"
  for _ in $(seq 1 60); do
    response="$(
      curl -fsS \
        -u elastic:elastic \
        -H "Content-Type: application/json" \
        -d "$query" \
        "http://localhost:9200/${pattern}/_count?allow_no_indices=true" \
        2>/dev/null || true
    )"
    if [[ -n "$response" ]]; then
      count="$(
        python3 -c \
          'import json, sys; print(json.load(sys.stdin).get("count", 0))' \
          <<<"$response"
      )"
      if [[ "$count" -gt 0 ]]; then
        echo "    $description: $count"
        return 0
      fi
    fi
    sleep 1
  done
  echo "no $description appeared in $pattern" >&2
  return 1
}

run_case() (
  set -Eeuo pipefail

  local case_name="$1"
  local file
  file="$(compose_file "$case_name")"
  local project="project-name-e2e-${case_name}"

  cleanup() {
    local status=$?
    if [[ $status -ne 0 ]]; then
      docker compose -p "$project" -f "$file" logs --no-color || true
    fi
    docker compose -p "$project" -f "$file" \
      down --volumes --remove-orphans >/dev/null 2>&1 || true
    exit "$status"
  }
  trap cleanup EXIT

  echo "==> E2E: $case_name"
  docker compose -p "$project" -f "$file" \
    down --volumes --remove-orphans >/dev/null 2>&1 || true
  docker compose -p "$project" -f "$file" \
    up --detach --build --wait --wait-timeout 180

  local frontend
  frontend="$(curl -fsS http://localhost:8080/)"
  if [[ "$frontend" != *"<title>Project / Name — Make something strange</title>"* ]] ||
    [[ "$frontend" != *"/assets/index-"* ]]; then
    echo "frontend did not return the expected application shell" >&2
    return 1
  fi

  if [[ "$case_name" == "postgres-keycloak" ]]; then
    local contributor_token
    local moderator_token
    contributor_token="$(wait_for_keycloak_token contributor contributor)"
    moderator_token="$(wait_for_keycloak_token moderator moderator)"

    E2E_BASE_URL=http://localhost:8080 \
      E2E_AUTH_MODE=bearer \
      E2E_CONTRIBUTOR_TOKEN="$contributor_token" \
      E2E_MODERATOR_TOKEN="$moderator_token" \
      E2E_EXPECT_CONTRIBUTOR_FORBIDDEN=true \
      go test -tags=e2e -count=1 ./tests/e2e
  else
    local token
    token="$(wait_for_static_token "$project" "$file")"

    E2E_BASE_URL=http://localhost:8080 \
      E2E_AUTH_MODE=static \
      E2E_CONTRIBUTOR_TOKEN="$token" \
      E2E_MODERATOR_TOKEN="$token" \
      go test -tags=e2e -count=1 ./tests/e2e

    if [[ "$case_name" == "elastic-otel" ]]; then
      wait_for_elastic_signal "traces-*"
      wait_for_elastic_signal "metrics-*"
      wait_for_elastic_signal "logs-*"
      wait_for_elastic_query \
        "traces-*" \
        '{"query":{"term":{"name":"auth.verify"}}}' \
        "authentication spans"
      wait_for_elastic_query \
        "traces-*" \
        '{"query":{"term":{"name":"wordstore.get_words"}}}' \
        "word store spans"
      wait_for_elastic_query \
        "metrics-*" \
        '{"query":{"exists":{"field":"metrics.http.server.request.duration"}}}' \
        "HTTP server metrics"
      wait_for_elastic_query \
        "metrics-*" \
        '{"query":{"exists":{"field":"metrics.project.wordstore.operations"}}}' \
        "word store metrics"
      wait_for_elastic_query \
        "metrics-*" \
        '{"query":{"exists":{"field":"metrics.go.goroutine.count"}}}' \
        "Go runtime metrics"
      wait_for_elastic_query \
        "logs-*" \
        '{"query":{"term":{"attributes.result":"unauthenticated"}}}' \
        "authorization audit logs"
      wait_for_elastic_query \
        "logs-*" \
        '{"query":{"term":{"resource.attributes.service.name":"postgresql"}}}' \
        "PostgreSQL server logs"
    fi
  fi
)

for case_name in "${CASES[@]}"; do
  run_case "$case_name"
done
