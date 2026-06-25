#!/usr/bin/env bash
# Bootstrap tenant_schema_versions V30 (ejecutar UNA vez por entorno).
set -euo pipefail

BASE_DIR="${TUKIFAC_BASE:-/opt/tukifac}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.production.yml}"
CONTAINER="${TUKIFAC_CONTAINER:-tukifac-backend-go}"

cd "${BASE_DIR}"

if ! docker ps --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  echo "ERROR: contenedor ${CONTAINER} no está en ejecución"
  exit 1
fi

echo "==> migrate-init-versions (baseline V30, idempotente)"
docker compose -f "${COMPOSE_FILE}" exec -T backend-go ./tukifac-api migrate-init-versions

echo "==> migrate-bump-target (target V31)"
docker compose -f "${COMPOSE_FILE}" exec -T backend-go ./tukifac-api migrate-bump-target

echo "OK. Active el cron: deploy/scripts/migrate-fleet.sh"
