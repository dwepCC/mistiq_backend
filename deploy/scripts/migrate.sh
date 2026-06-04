#!/usr/bin/env bash
# Migración BD central solamente (post-deploy). Fleet de tenants: migrate-fleet.sh
set -euo pipefail

BASE_DIR="${MISTIQ_BASE:-/opt/mistiq}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.production.yml}"
CONTAINER="${MISTIQ_CONTAINER:-mistiq-backend-go}"
CMD="${MIGRATE_CMD:-migrate-central}"

cd "${BASE_DIR}"

echo "==> Ejecutando migrate CENTRAL (no incluye fleet de tenants)"
echo "    Para tenants: bash deploy/scripts/migrate-fleet.sh"
echo "    Ver: docs/MIGRATIONS-SaaS.md"

if docker ps --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  docker compose -f "${COMPOSE_FILE}" exec -T backend-go ./mistiq-api "${CMD}"
else
  echo "==> Contenedor no activo; usando run --rm con imagen actual"
  docker compose -f "${COMPOSE_FILE}" run --rm --no-deps backend-go ./mistiq-api "${CMD}"
fi
