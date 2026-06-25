#!/usr/bin/env bash
# Deploy completo en VPS: pull → migrate-central → restart → health
# Uso:
#   ./deploy/scripts/deploy.sh
#   TUKIFAC_IMAGE=ghcr.io/org/repo:abc123 ./deploy/scripts/deploy.sh
#   SKIP_MIGRATE=1 ./deploy/scripts/deploy.sh
set -euo pipefail

BASE_DIR="${TUKIFAC_BASE:-/opt/tukifac}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.production.yml}"
ENV_FILE="${ENV_FILE:-.env}"
CONTAINER="${TUKIFAC_CONTAINER:-tukifac-backend-go}"
SKIP_MIGRATE="${SKIP_MIGRATE:-0}"
SKIP_PULL="${SKIP_PULL:-0}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

cd "${BASE_DIR}"

mkdir -p .deploy

if [[ ! -f "${ENV_FILE}" ]]; then
  echo "ERROR: falta ${BASE_DIR}/${ENV_FILE}"
  exit 1
fi

if [[ ! -f "${COMPOSE_FILE}" ]]; then
  echo "ERROR: falta ${BASE_DIR}/${COMPOSE_FILE}"
  exit 1
fi

# Imagen explícita (CI) o la definida en .env
if [[ -n "${TUKIFAC_IMAGE:-}" ]]; then
  if grep -q '^TUKIFAC_IMAGE=' "${ENV_FILE}"; then
    sed -i "s|^TUKIFAC_IMAGE=.*|TUKIFAC_IMAGE=${TUKIFAC_IMAGE}|" "${ENV_FILE}"
  else
    echo "TUKIFAC_IMAGE=${TUKIFAC_IMAGE}" >> "${ENV_FILE}"
  fi
fi

CURRENT_IMAGE="$(grep -E '^TUKIFAC_IMAGE=' "${ENV_FILE}" | cut -d= -f2- | tr -d '\r\n')"
if [[ -z "${CURRENT_IMAGE}" ]]; then
  echo "ERROR: TUKIFAC_IMAGE no definido en ${ENV_FILE}"
  exit 1
fi

# Guardar imagen actual para rollback (antes de pull)
if docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  RUNNING_IMAGE="$(docker inspect --format='{{.Config.Image}}' "${CONTAINER}" 2>/dev/null || true)"
  if [[ -n "${RUNNING_IMAGE}" ]] && [[ "${RUNNING_IMAGE}" != "${CURRENT_IMAGE}" ]]; then
    echo "${RUNNING_IMAGE}" > .deploy/previous-image
  elif [[ -f .deploy/current-image ]]; then
    cp .deploy/current-image .deploy/previous-image 2>/dev/null || true
  fi
fi

echo "=============================================="
echo " Tukifac Backend — Deploy"
echo " Imagen: ${CURRENT_IMAGE}"
echo "=============================================="

if [[ "${SKIP_PULL}" != "1" ]]; then
  echo "==> Pull imagen"
  docker compose -f "${COMPOSE_FILE}" pull backend-go
fi

if [[ "${SKIP_MIGRATE}" != "1" ]]; then
  echo "==> Migración BD central (ANTES del restart, imagen nueva)"
  if ! docker compose -f "${COMPOSE_FILE}" run --rm --no-deps backend-go ./tukifac-api migrate-central; then
    echo "ERROR: migrate-central falló"
    exit 1
  fi
  echo "    Fleet tenants: usar deploy/scripts/migrate-fleet.sh o cron (docs/MIGRATIONS-SaaS.md)"
else
  echo "==> SKIP_MIGRATE=1 — migrate central omitido"
fi

echo "==> Recrear contenedor (downtime breve ~2-5s)"
docker compose -f "${COMPOSE_FILE}" up -d --no-deps --force-recreate backend-go

echo "${CURRENT_IMAGE}" > .deploy/current-image

echo "==> Esperando arranque del contenedor"
sleep 5
if ! docker ps --format '{{.Names}}' | grep -qx "${CONTAINER}"; then
  echo "ERROR: el contenedor no arrancó"
  docker compose -f "${COMPOSE_FILE}" logs --tail=50 backend-go
  exit 1
fi

echo "==> Health check"
if [[ -x "${SCRIPT_DIR}/health-check.sh" ]]; then
  bash "${SCRIPT_DIR}/health-check.sh"
else
  curl -sf http://127.0.0.1:3000/health >/dev/null
  echo "OK"
fi

echo "=============================================="
echo " Deploy completado"
echo "=============================================="
