#!/usr/bin/env bash
# Migración BD central solamente (post-deploy). Fleet de tenants: migrate-fleet.sh
set -euo pipefail

BASE_DIR="${TUKIFAC_BASE:-/opt/tukifac}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.production.yml}"
CONTAINER="${TUKIFAC_CONTAINER:-tukifac-backend-go}"
CMD="${MIGRATE_CMD:-migrate-central}"

cd "${BASE_DIR}"

echo "==> Ejecutando migrate CENTRAL (no incluye fleet de tenants)"
echo "    Para tenants: bash deploy/scripts/migrate-fleet.sh"
echo "    Ver: docs/MIGRATIONS-SaaS.md"

if docker ps --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  docker compose -f "${COMPOSE_FILE}" exec -T backend-go ./tukifac-api "${CMD}"
else
  echo "==> Contenedor no activo; usando run --rm con imagen actual"
  docker compose -f "${COMPOSE_FILE}" run --rm --no-deps backend-go ./tukifac-api "${CMD}"
fi
