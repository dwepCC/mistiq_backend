#!/usr/bin/env bash
# Cron: migrate-fleet-cron con lock (flock en host + Redis/DB en API).
# Uso: */5 * * * * /opt/tukifac/deploy/scripts/migrate-fleet.sh
set -euo pipefail

BASE_DIR="${TUKIFAC_BASE:-/opt/tukifac}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.production.yml}"
CONTAINER="${TUKIFAC_CONTAINER:-tukifac-backend-go}"
LOCKFILE="${MIGRATE_LOCKFILE:-/tmp/tukifac-migrate-fleet.lock}"
TIMEOUT="${MIGRATE_TIMEOUT_SEC:-3600}"
LOG_DIR="${MIGRATE_LOG_DIR:-/opt/tukifac/logs}"
LOG_FILE="${LOG_DIR}/migrate-fleet.log"
WORKERS="${MIGRATE_WORKERS:-4}"
LIMIT="${MIGRATE_LIMIT:-100}"

mkdir -p "${LOG_DIR}"

cd "${BASE_DIR}"

# Fallback host: evita dos docker exec concurrentes en el mismo VPS.
exec 9>"${LOCKFILE}"
if ! flock -n 9; then
  exit 0
fi

if ! docker ps --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  echo "$(date -Iseconds) [error] contenedor ${CONTAINER} no está en ejecución" >> "${LOG_FILE}"
  exit 1
fi

echo "$(date -Iseconds) [start] migrate-fleet-cron workers=${WORKERS} limit=${LIMIT}" >> "${LOG_FILE}"

set +e

timeout "${TIMEOUT}" docker exec "${CONTAINER}" \
  ./tukifac-api migrate-fleet-cron \
  --workers="${WORKERS}" \
  --limit="${LIMIT}" >> "${LOG_FILE}" 2>&1

RC=$?

# rc!=0 indica fallo real (tenant o circuit breaker); con fleet OK y sin pendientes debe ser 0.
if [ "${RC}" -ne 0 ]; then
  echo "$(date -Iseconds) [warn] migrate-fleet-cron exited rc=${RC}" >> "${LOG_FILE}"
fi

set -e

echo "$(date -Iseconds) [done] rc=${RC}" >> "${LOG_FILE}"
exit "${RC}"