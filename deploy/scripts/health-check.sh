#!/usr/bin/env bash
# Verifica liveness y readiness del backend.
set -euo pipefail

BASE_DIR="${TUKIFAC_BASE:-/opt/tukifac}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.production.yml}"
CONTAINER="${TUKIFAC_CONTAINER:-tukifac-backend-go}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:3000/health}"
MAX_WAIT="${MAX_WAIT:-90}"
INTERVAL="${INTERVAL:-3}"

cd "${BASE_DIR}"

echo "==> Estado del contenedor"
if ! docker ps --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  echo "ERROR: contenedor ${CONTAINER} no está en ejecución"
  docker compose -f "${COMPOSE_FILE}" ps || true
  exit 1
fi

docker inspect --format='Estado: {{.State.Status}} | Health: {{if .State.Health}}{{.State.Health.Status}}{{else}}n/a{{end}}' "${CONTAINER}" 2>/dev/null || true

echo "==> Esperando readiness (${HEALTH_URL})"
elapsed=0
while [[ ${elapsed} -lt ${MAX_WAIT} ]]; do
  if curl -sf "${HEALTH_URL}" >/dev/null 2>&1; then
    echo "OK: health check passed"
    curl -s "${HEALTH_URL}" | head -c 500
    echo ""
    exit 0
  fi
  sleep "${INTERVAL}"
  elapsed=$((elapsed + INTERVAL))
done

echo "ERROR: health check no respondió en ${MAX_WAIT}s"
echo "Últimos logs:"
docker compose -f "${COMPOSE_FILE}" logs --tail=40 backend-go || true
exit 1
