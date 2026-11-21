#!/usr/bin/env bash
# Deploy completo en VPS: pull → migrate-central → migrate-fleet (todos los tenants) → restart → health
# Uso:
#   ./deploy/scripts/deploy.sh
#   MISTIQ_IMAGE=ghcr.io/org/repo:abc123 ./deploy/scripts/deploy.sh
#   SKIP_MIGRATE=1 ./deploy/scripts/deploy.sh
#   ALLOW_FLEET_FAILURES=1 ./deploy/scripts/deploy.sh   # desplegar aunque falle algún tenant
set -euo pipefail

BASE_DIR="${TUKIFAC_BASE:-/opt/mistiq}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.production.yml}"
ENV_FILE="${ENV_FILE:-.env}"
CONTAINER="${TUKIFAC_CONTAINER:-mistiq-backend-go}"
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
if [[ -n "${MISTIQ_IMAGE:-}" ]]; then
  if grep -q '^MISTIQ_IMAGE=' "${ENV_FILE}"; then
    sed -i "s|^MISTIQ_IMAGE=.*|MISTIQ_IMAGE=${MISTIQ_IMAGE}|" "${ENV_FILE}"
  else
    echo "MISTIQ_IMAGE=${MISTIQ_IMAGE}" >> "${ENV_FILE}"
  fi
fi

CURRENT_IMAGE="$(grep -E '^MISTIQ_IMAGE=' "${ENV_FILE}" | cut -d= -f2- | tr -d '\r\n')"
if [[ -z "${CURRENT_IMAGE}" ]]; then
  echo "ERROR: MISTIQ_IMAGE no definido en ${ENV_FILE}"
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

  # Esquema de TODOS los tenants con la imagen nueva ANTES del restart; si se deja al cron el código
  # nuevo corre hasta ~1 h contra tenants sin la columna nueva ("Unknown column"). El contenedor viejo
  # sigue atendiendo mientras tanto (migraciones solo aditivas, ver docs/MIGRATIONS-SaaS.md). Mismo
  # flock que el cron: espera el ciclo en curso y lo pausa. Si falla, no se reinicia.
  echo "==> Migración de todos los tenants (ANTES del restart, imagen nueva)"
  if ! flock -w 900 "${MIGRATE_LOCKFILE:-/tmp/tukifac-migrate-fleet.lock}" \
    docker compose -f "${COMPOSE_FILE}" run --rm --no-deps backend-go \
    ./tukifac-api migrate-fleet --limit=100000 --workers=8 --active-only=false --skip-drift-scan; then
    if [[ "${ALLOW_FLEET_FAILURES:-0}" == "1" ]]; then
      echo "    migrate-fleet con fallos — ALLOW_FLEET_FAILURES=1, se continúa"
    else
      echo "ERROR: migrate-fleet falló — deploy abortado, sigue corriendo la versión anterior"
      echo "       (revisar los tenants listados; ALLOW_FLEET_FAILURES=1 para desplegar igual)"
      exit 1
    fi
  fi
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
