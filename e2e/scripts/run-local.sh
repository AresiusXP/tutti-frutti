#!/usr/bin/env bash
# One command for a local run: build and start the compose stack, run the suite against it and
# always tear the stack down again.
#
#   cd e2e && npm run test:stack                         # everything
#   cd e2e && npm run test:stack -- -g "kicked" --headed # extra arguments go to `playwright test`
#
# Environment (all optional): EDGE_PORT (8000), POSTGRES_PORT (5432), E2E_COMPOSE_PROJECT (tutti-frutti),
# COMPOSE_FILE (the repo's docker-compose.yml), KEEP_STACK=1 (leave the stack running afterwards).
set -euo pipefail

cd "$(dirname "$0")/.."
root="$(cd .. && pwd)"

export EDGE_PORT="${EDGE_PORT:-8000}"
export POSTGRES_PORT="${POSTGRES_PORT:-5432}"
export E2E_COMPOSE_PROJECT="${E2E_COMPOSE_PROJECT:-tutti-frutti}"
export E2E_BASE_URL="${E2E_BASE_URL:-http://localhost:${EDGE_PORT}}"
export COMPOSE_FILE="${COMPOSE_FILE:-${root}/docker-compose.yml}"

compose() { docker compose -p "${E2E_COMPOSE_PROJECT}" "$@"; }

cleanup() {
  if [ "${KEEP_STACK:-}" != "1" ]; then
    compose down -v >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

compose up -d --build --wait
npx playwright test "$@"
